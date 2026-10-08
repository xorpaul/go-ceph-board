#!/usr/bin/env python3
# ceph_mds_sr_lag_textfile.py — emit CephFS standby-replay journal lag as
# Prometheus textfile metrics
#
# Shows how far each standby-replay MDS trails the journal of the active rank
# it follows, from the mds_log journal positions in
# `ceph tell mds.<name> perf dump mds_log`. The mgr prometheus module does not
# export these position counters. go-ceph-board shows them in the Jrnl and Lag
# columns of the per-filesystem MDS table.
#
# Run on any Ceph mon or admin host as root (admin keyring required for
# `ceph tell`), e.g. from a minutely systemd timer or cron job. Output is
# picked up by node_exporter's textfile collector; add the host to the
# go-ceph-board `extra_hosts` list so it is scraped. Ranks are queried in
# parallel.
#
# Metrics emitted:
#   ceph_mds_sr_lag_bytes{fs_name, rank, ceph_daemon, active_daemon}
#       Standby-replay wrpos - rdpos: journal written by the active but not yet
#       replayed. A few KB is normal; a value that keeps growing means the
#       standby-replay cannot keep up.
#   ceph_mds_sr_margin_bytes{fs_name, rank, ceph_daemon, active_daemon}
#       Standby-replay rdpos - active expos: how much further it can fall
#       behind before the active trims journal it has not read. At <= 0 it
#       respawns with "respawning since we fell behind journal". An immediate
#       trim (`ceph tell mds.<fs>:<rank> flush journal`) can collapse this
#       margin at once, even for a standby-replay with near-zero lag.
#   ceph_mds_journal_live_bytes{fs_name, rank, ceph_daemon}
#       Active wrpos - expos: size of the untrimmed journal.
#   ceph_mds_sr_present{fs_name, rank}
#       1 if the rank has a standby-replay daemon, 0 otherwise.
#   ceph_mds_sr_lag_query_errors_total / ceph_mds_sr_lag_textfile_error
#       Only present when some `ceph tell` / `ceph fs dump` failed.
#
# The standby-replay is queried before the active. Positions only grow, so
# a later active expos can only make the margin look smaller, never larger.
#
# ceph_daemon / active_daemon use the "mds.<name>" form of the mgr prometheus
# module, so consumers can join on it directly.
#
# Usage:
#   CEPH_TEXTFILE_DIR=/custom/path ./ceph_mds_sr_lag_textfile.py

import json
import os
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path


# ── textfile dir detection (same order as ceph_mdsmap_textfile.sh) ─────────

def _textfile_dir():
    """CEPH_TEXTFILE_DIR, else the running node_exporter's
    --collector.textfile.directory, else a cephadm node-exporter's directory,
    else /var/lib/prometheus/node-exporter."""
    override = os.environ.get("CEPH_TEXTFILE_DIR")
    if override:
        return Path(override)
    try:
        pids = subprocess.run(["pgrep", "-f", "prometheus-node-exporter"],
                              capture_output=True, text=True, timeout=10).stdout.split()
        if pids:
            args = Path(f"/proc/{pids[0]}/cmdline").read_bytes().split(b"\0")
            for a in args:
                if a.startswith(b"--collector.textfile.directory="):
                    return Path(a.split(b"=", 1)[1].decode())
    except Exception:
        pass
    try:
        host = subprocess.run(["hostname", "-s"], capture_output=True, text=True).stdout.strip()
        units = subprocess.run(
            ["systemctl", "list-units", "--no-legend", "--no-pager", "--plain",
             "ceph-*@node-exporter*.service"],
            capture_output=True, text=True, timeout=10,
        ).stdout
        m = re.search(
            r"ceph-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"
            r"@node-exporter",
            units,
        )
        if m:
            fsid = m.group(1)
            return Path(f"/var/lib/ceph/{fsid}/node-exporter.{host}/etc/node-exporter")
    except Exception:
        pass
    return Path("/var/lib/prometheus/node-exporter")


# ── ceph helpers ──────────────────────────────────────────────────────────────

def _ceph(*args, timeout=30):
    result = subprocess.run(
        ["ceph"] + list(args) + ["--format", "json"],
        capture_output=True, text=True, timeout=timeout,
    )
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip())
    return json.loads(result.stdout)


def _ranks():
    """Return {(fs_name, rank): {"active": name|None, "sr": name|None}}."""
    dump = _ceph("fs", "dump")
    out = {}
    for fs in dump.get("filesystems", []):
        mdsmap = fs.get("mdsmap", {})
        fsname = mdsmap.get("fs_name", "")
        for info in mdsmap.get("info", {}).values():
            state = info.get("state")
            rank = info.get("rank", -1)
            if rank < 0:
                continue
            slot = out.setdefault((fsname, rank), {"active": None, "sr": None})
            if state == "up:active":
                slot["active"] = info["name"]
            elif state == "up:standby-replay":
                slot["sr"] = info["name"]
    return out


def _mds_log(daemon_name, timeout=30):
    return _ceph("tell", f"mds.{daemon_name}", "perf", "dump", "mds_log", timeout=timeout)["mds_log"]


# ── main ──────────────────────────────────────────────────────────────────────

def main():
    textfile_dir = _textfile_dir()
    textfile_dir.mkdir(parents=True, exist_ok=True)
    prom_file = textfile_dir / "ceph_mds_sr_lag.prom"

    lines = [
        "# HELP ceph_mds_sr_lag_bytes Standby-replay journal bytes written by the active but not yet replayed (wrpos - rdpos)",
        "# TYPE ceph_mds_sr_lag_bytes gauge",
        "# HELP ceph_mds_sr_margin_bytes Standby-replay read position minus the active's expire position; <= 0 means it fell behind the trimmed journal",
        "# TYPE ceph_mds_sr_margin_bytes gauge",
        "# HELP ceph_mds_journal_live_bytes Active MDS untrimmed journal size (wrpos - expos)",
        "# TYPE ceph_mds_journal_live_bytes gauge",
        "# HELP ceph_mds_sr_present 1 if the rank has a standby-replay daemon",
        "# TYPE ceph_mds_sr_present gauge",
    ]

    try:
        ranks = _ranks()
    except Exception as e:
        lines.append(f"# ceph fs dump failed: {e}")
        lines.append("ceph_mds_sr_lag_textfile_error 1")
        _atomic_write(prom_file, lines)
        print(f"ceph_mds_sr_lag_textfile.py: ceph fs dump failed: {e}", file=sys.stderr)
        sys.exit(1)

    results = {}
    errors = {}

    def query(key, slot):
        try:
            sr = _mds_log(slot["sr"]) if slot["sr"] else None
            active = _mds_log(slot["active"]) if slot["active"] else None
            results[key] = (sr, active)
        except Exception as exc:
            errors[key] = str(exc)

    with ThreadPoolExecutor(max_workers=16) as pool:
        futures = [pool.submit(query, k, s) for k, s in ranks.items()]
        for f in as_completed(futures):
            f.result()

    for (fs_name, rank), (sr, active) in sorted(results.items()):
        slot = ranks[(fs_name, rank)]
        lines.append(f'ceph_mds_sr_present{{fs_name="{fs_name}",rank="{rank}"}} {1 if sr else 0}')
        if active:
            lbl = f'fs_name="{fs_name}",rank="{rank}",ceph_daemon="mds.{slot["active"]}"'
            lines.append(f"ceph_mds_journal_live_bytes{{{lbl}}} {active['wrpos'] - active['expos']}")
        if sr:
            active_lbl = f'mds.{slot["active"]}' if slot["active"] else ""
            lbl = (f'fs_name="{fs_name}",rank="{rank}",ceph_daemon="mds.{slot["sr"]}",'
                   f'active_daemon="{active_lbl}"')
            lines.append(f"ceph_mds_sr_lag_bytes{{{lbl}}} {sr['wrpos'] - sr['rdpos']}")
            if active:
                lines.append(f"ceph_mds_sr_margin_bytes{{{lbl}}} {sr['rdpos'] - active['expos']}")

    if errors:
        for (fs_name, rank), err in sorted(errors.items()):
            lines.append(f"# ERROR {fs_name}/{rank}: {err}")
        lines.append(f"ceph_mds_sr_lag_query_errors_total {len(errors)}")

    _atomic_write(prom_file, lines)


def _atomic_write(path, lines):
    tmp = path.with_suffix(".prom.tmp")
    tmp.write_text("\n".join(lines) + "\n")
    tmp.rename(path)


if __name__ == "__main__":
    main()
