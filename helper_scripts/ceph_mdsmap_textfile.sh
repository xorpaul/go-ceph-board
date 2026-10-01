#!/bin/bash
# ceph_mdsmap_textfile.sh — emit MDS map state as Prometheus textfile metrics
#
# Run on any Ceph mon host (or any host with ceph CLI + admin keyring).
# Install as a systemd timer or cron job; output is picked up by node_exporter's
# textfile collector at /var/lib/prometheus/node-exporter/.
# Uses python3 for JSON parsing (no jq dependency).
#
# Metrics emitted:
#   ceph_mdsmap_epoch{fs_name,fs_id}
#       Current MDS map epoch; advances on every MDS map change.
#   ceph_mds_rank_incarnation{ceph_daemon,fs_name,fs_id,rank,state}
#       Ceph incarnation counter per daemon/rank: 0 for standby-replay,
#       non-zero (incrementing) for active. go-ceph-board watches this
#       to detect rank reassignments and record their timestamps.
#   ceph_mds_rank_state_seq{ceph_daemon,fs_name,fs_id,rank}
#       State-sequence counter; increments on each state transition.
#   ceph_mds_rank_assigned{ceph_daemon,fs_name,fs_id,rank,state}
#       1 for every daemon currently holding a rank (active or standby-replay).
#       Carries the authoritative state label, which is empty in the ceph-mgr
#       Prometheus endpoint for cephadm-managed clusters.
#
# Usage:
#   CEPH_TEXTFILE_DIR=/custom/path ./ceph_mdsmap_textfile.sh

set -euo pipefail

# Auto-detect textfile directory from the running node_exporter process.
# Override with CEPH_TEXTFILE_DIR env var if needed.
# pgrep -f (full cmdline) not -x: binary name exceeds 15-char comm field limit.
_ne_dir=""
_ne_pid=$(pgrep -f 'prometheus-node-exporter' 2>/dev/null | head -1 || true)
if [ -n "$_ne_pid" ]; then
    _ne_dir=$(tr '\0' '\n' < "/proc/${_ne_pid}/cmdline" 2>/dev/null \
        | grep -oP '(?<=--collector\.textfile\.directory=)\S+' | head -1 || true)
fi
TEXTFILE_DIR="${CEPH_TEXTFILE_DIR:-${_ne_dir:-/var/lib/prometheus/node-exporter}}"
TMP_FILE="${TEXTFILE_DIR}/ceph_mdsmap.prom.tmp"
PROM_FILE="${TEXTFILE_DIR}/ceph_mdsmap.prom"

mkdir -p "$TEXTFILE_DIR"

# Pipe ceph fs dump directly to python3 via stdin — passing the full JSON as
# argv[1] exceeds ARG_MAX (~2MB) when the cluster has many filesystems/daemons.
ceph fs dump -f json 2>/dev/null | python3 -c '
import sys, json

dump = json.load(sys.stdin)

print("# HELP ceph_mdsmap_epoch Current MDS map epoch; advances on every MDS map change")
print("# TYPE ceph_mdsmap_epoch gauge")
print("# HELP ceph_mds_rank_incarnation Ceph MDS incarnation counter; 0 for standby-replay, non-zero and incrementing for active - changes when a new daemon takes the rank")
print("# TYPE ceph_mds_rank_incarnation gauge")
print("# HELP ceph_mds_rank_state_seq MDS daemon state-sequence counter; increments on each state transition")
print("# TYPE ceph_mds_rank_state_seq gauge")
print("# HELP ceph_mds_rank_assigned 1 for every daemon currently holding a rank (active or standby-replay)")
print("# TYPE ceph_mds_rank_assigned gauge")

for fs in dump.get("filesystems", []):
    mm     = fs["mdsmap"]
    fsname = mm["fs_name"]
    fsid   = str(fs["id"])
    epoch  = mm["epoch"]
    print(f"ceph_mdsmap_epoch{{fs_name=\"{fsname}\",fs_id=\"{fsid}\"}} {epoch}")
    for info in mm.get("info", {}).values():
        daemon      = info["name"]
        rank        = str(info["rank"])
        state       = info["state"]
        incarnation = info["incarnation"]
        state_seq   = info["state_seq"]
        labels = f"ceph_daemon=\"{daemon}\",fs_name=\"{fsname}\",fs_id=\"{fsid}\",rank=\"{rank}\""
        print(f"ceph_mds_rank_incarnation{{{labels},state=\"{state}\"}} {incarnation}")
        print(f"ceph_mds_rank_state_seq{{{labels}}} {state_seq}")
        print(f"ceph_mds_rank_assigned{{{labels},state=\"{state}\"}} 1")
' > "$TMP_FILE" || {
    printf '# ceph fs dump failed\nceph_mdsmap_textfile_error 1\n' > "$TMP_FILE"
    mv "$TMP_FILE" "$PROM_FILE"
    echo "ceph_mdsmap_textfile.sh: ceph fs dump or python3 failed" >&2
    exit 1
}

mv "$TMP_FILE" "$PROM_FILE"
