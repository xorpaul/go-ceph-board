#!/usr/bin/env bash
set -euo pipefail

usage() { echo "usage: $0 [--local] [<hostname>]" >&2; exit 1; }

LOCAL=false
HOST=""
for arg in "$@"; do
    case "$arg" in
        --local) LOCAL=true ;;
        --*) usage ;;
        *) HOST="$arg" ;;
    esac
done

if $LOCAL; then
    [[ -z "$HOST" ]] && HOST=$(hostname -s)
else
    [[ -z "$HOST" ]] && usage
fi

OUTDIR="mem-pressure-${HOST}-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUTDIR"

echo "Collecting from ${LOCAL:+local /}${HOST} -> $OUTDIR"

run() {
    local label=$1; shift
    echo "  $label..."
    if $LOCAL; then
        bash -c "$*" > "$OUTDIR/${label}.txt" 2>&1 || true
    else
        ssh "$HOST" "$@" > "$OUTDIR/${label}.txt" 2>&1 || true
    fi
}

run vmstat         "vmstat 1 5"
run meminfo        "cat /proc/meminfo"
run slabtop        "slabtop -o"
run numastat       "numastat -m"
run ceph-mds-cache "sudo ceph tell 'mds.*' cache status"

echo "Done. Files in $OUTDIR/"
ls -lh "$OUTDIR/"
