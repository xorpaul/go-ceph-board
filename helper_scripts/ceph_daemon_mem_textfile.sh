#!/bin/bash
set -euo pipefail

HOST=$(hostname -s)

# Detect deployment mode: cephadm (containerised) vs package-based.
# cephadm names units as ceph-<fsid>@<daemon>.service; package installs use ceph-osd@<id>.service etc.
FSID=$(systemctl list-units --no-legend --no-pager --plain 'ceph-*@*.service' 2>/dev/null \
    | awk '{print $1}' \
    | grep -oP 'ceph-\K[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' \
    | head -1 || true)

if [[ -n "$FSID" ]]; then
    # cephadm: daemons run in containers but the host node_exporter may be the
    # Debian package (not the cephadm-managed container). Write to the fixed
    # package path so the host node_exporter can expose the metrics, and also
    # write to the cephadm container path in case the containerised exporter is
    # used in the future.
    TEXTFILE_DIR="${CEPH_TEXTFILE_DIR:-/var/lib/prometheus/node-exporter}"
    CEPHADM_TEXTFILE_DIR="/var/lib/ceph/${FSID}/node-exporter.${HOST}/etc/node-exporter"
    UNIT_PATTERN="ceph-${FSID}@*.service"
    get_daemon_name() {
        # ceph-<fsid>@mds.m05.mds1.hzyqas.service -> mds.m05.mds1.hzyqas
        echo "$1" | sed "s/ceph-${FSID}@//" | sed 's/\.service$//'
    }
else
    # package: textfile dir must be configured; default matches common Debian setups
    TEXTFILE_DIR="${CEPH_TEXTFILE_DIR:-/var/lib/prometheus/node-exporter}"
    CEPHADM_TEXTFILE_DIR=""
    UNIT_PATTERN="ceph-*@*.service"
    get_daemon_name() {
        # ceph-osd@137.service -> osd.137
        # ceph-mds@mds3.service -> mds.mds3
        local base
        base=$(echo "$1" | sed 's/\.service$//')   # strip .service
        local type instance
        type=$(echo "$base" | sed 's/ceph-\([^@]*\)@.*/\1/')
        instance=$(echo "$base" | sed 's/.*@//')
        echo "${type}.${instance}"
    }
fi

TMP_FILE="${TEXTFILE_DIR}/ceph_daemon_mem.prom.tmp"
PROM_FILE="${TEXTFILE_DIR}/ceph_daemon_mem.prom"

mkdir -p "$TEXTFILE_DIR"

{
    echo "# HELP ceph_daemon_memory_bytes Ceph daemon memory usage in bytes (from cgroup MemoryCurrent via systemctl)"
    echo "# TYPE ceph_daemon_memory_bytes gauge"
    echo "# HELP ceph_daemon_start_time_seconds Unix timestamp when the Ceph daemon systemd service last entered active state"
    echo "# TYPE ceph_daemon_start_time_seconds gauge"
    systemctl list-units --no-legend --no-pager --plain "$UNIT_PATTERN" 2>/dev/null \
      | awk '{print $1}' \
      | while read -r unit; do
        daemon=$(get_daemon_name "$unit")

        mem=$(systemctl show "$unit" --property=MemoryCurrent --value 2>/dev/null || echo "[not set]")
        if [[ "$mem" != "[not set]" && "$mem" != "18446744073709551615" ]]; then
            echo "ceph_daemon_memory_bytes{ceph_daemon=\"${daemon}\"} ${mem}"
        fi

        ts_str=$(systemctl show "$unit" --property=ActiveEnterTimestamp --value 2>/dev/null || true)
        if [[ -n "$ts_str" && "$ts_str" != "n/a" ]]; then
            epoch=$(date -d "$ts_str" +%s 2>/dev/null || true)
            if [[ -n "$epoch" && "$epoch" != "0" ]]; then
                echo "ceph_daemon_start_time_seconds{ceph_daemon=\"${daemon}\"} ${epoch}"
            fi
        fi
    done
} > "$TMP_FILE"

mv "$TMP_FILE" "$PROM_FILE"

# On cephadm hosts also copy to the container textfile dir so the cephadm-managed
# node_exporter (if ever used) picks up the same data.
if [[ -n "$CEPHADM_TEXTFILE_DIR" ]]; then
    mkdir -p "$CEPHADM_TEXTFILE_DIR"
    cp "$PROM_FILE" "${CEPHADM_TEXTFILE_DIR}/ceph_daemon_mem.prom"
fi
