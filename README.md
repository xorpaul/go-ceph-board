# go-ceph-board

A real-time Ceph cluster monitoring dashboard written in Go. Pulls data directly from the **ceph-mgr Prometheus exporter** (no credentials required) and renders a live overview board in the browser.

Designed as a companion to [go-elastic-board](https://github.com/xorpaul/go-elastic-board) — same architecture, same deployment model, different backend.

## Features

### Cluster Health
- HEALTH_OK / HEALTH_WARN / HEALTH_ERR status with pulsing color indicator
- Inferred health detail line on WARN/ERR: OSDs down, OSDs up-but-out, quorum loss, PG degraded/undersized/recovering/backfilling — ERR in red, WARN in amber
- OSD up/in/total counts with sparkline history
- PG state summary: total (correctly summed across all pools for Ceph Reef+), clean, degraded, recovering
- Cluster storage usage with sparkline history
- Monitor quorum count

### Time-Series Charts
- PG states over time: clean, degraded, recovering, backfilling, undersized
- OSD status over time: up, in, total
- Cluster usage % over time (30-point rolling window)

### Pool Table
- Sortable by any column — click header to sort ascending/descending; re-renders from cache without a new fetch
- Pool type badge: `rep×N` (green) for replicated, `ec k+m` (blue) for erasure-coded, with tooltip explaining overhead
- Used / available / % used with color-coded bar; TiB values get a red badge, GiB an orange badge for instant visual distinction
- Read and write IOPS (rate-calculated from Prometheus counters across refresh intervals)
- Read and write throughput in MB/s
- Object count per pool

### OSD Host Table
- Rows per **host** (not per OSD — clusters with 20+ OSDs per host stay readable)
- Grouped and color-coded by site, auto-detected from hostname or CRUSH location labels
- Aggregated disk usage per host with color-coded bar
- Average apply and commit latency per host, color-coded by severity
- CPU utilisation % and memory usage bar per host (from node_exporter, non-fatal if unavailable); core count subscript and CPU model tooltip on each bar
- Per-OSD status badges: green = up+in, orange = up+out, red = down
- MDS badge column showing active ranks (green) and standby count per host

### Monitor Table
- Quorum status per daemon with site badge
- ★ Paxos-leader heuristic (daemon with most election wins)
- **MGR** badge on the monitor host currently running the active ceph-mgr
- Clock Skew (max peer skew in ms, colour-coded at 10/50 ms thresholds)
- Peer RTT (max round-trip to peer monitors)
- Elections (cumulative wins/losses/calls)
- RocksDB store size and Quorum Age
- CPU utilisation % and memory bar per monitor (from node_exporter)

### MDS Section
- **MDS rank-0 site indicator** — compact strip showing which site holds MDS rank 0 per filesystem, marked with ★
- **MDS Hosts table** — per-host CPU/memory bars with active/standby count badges per filesystem; memory RSS from `ceph_mds_mem_rss`; shows `—` (with a tooltip) when the mgr emits no RSS (e.g. `exclude_perf_counters = true`)
- **Amber banner** when `ceph_mds_mem_rss` is absent but active daemons exist, explaining how to re-enable the metric
- **MDS Daemons table** — every MDS daemon with rank, host, site, and live session count
- **MDS Session Distribution (Sankey)** — flow diagram mapping CephFS filesystems to MDS host servers; band width proportional to session count; hover shows exact counts
- Sessions column in MDS filesystem and MDS Hosts tables (from `ceph_mds_sessions_session_count`)
- **MDS rank assignment tracking** — the "Since" column in the daemon detail grid shows the time a daemon took its rank (i.e. the last MDS failover for that rank slot), sourced from the `/rank-assignments` endpoint. Falls back to `ceph_daemon_start_time_seconds` (systemd service start) when rank assignment data is unavailable. Rows where "Since" is under 1 hour are highlighted red — a quick visual signal for recent failovers. Requires the `ceph_mdsmap_textfile.sh` textfile helper on an admin/mon host; see [MDS Rank Assignment Tracking](#mds-rank-assignment-tracking) below.

### Multi-Cluster Support
- Named clusters under `ceph:` in the config (e.g. `EU:`, `US:`)
- Cluster switcher dropdown in the header with country flag emoji (🇪🇺 / 🇺🇸) for EU/US-prefixed names
- Switching tears down all charts and history buffers and restarts the fetch loop against the selected cluster
- Single-cluster deployments see no UI change

### Security
- Optional TLS with client certificate authentication (who may view the board)
- Automatic certificate hot-reload via fsnotify — no restart needed on renewal
- Configurable allowed CN list, with optional help links on the 401/403 pages
- Optional plain-HTTP health-check port (`tls.health_check_port`) for HAProxy without client certs
- Run without a config file for plain HTTP (development / trusted network)

### Usability
- Sticky quick-navigation bar — pill links jump to Health, OSD Hosts, Monitors, MDS, Pools, Session Map, MDS Daemons
- Dark mode by default, light mode toggle
- Configurable refresh interval
- Single self-contained binary — all static assets embedded, nothing to deploy separately
- Cache warmup on startup — mgr and node metrics pre-fetched for all clusters so the first browser request is never cold

## No credentials needed for Ceph

The ceph-mgr Prometheus exporter has no authentication by default. `go-ceph-board` simply does a plain HTTP GET to `/metrics` on port 9283. If you can reach that port, you get the metrics.

## Prerequisites

- Go 1.25 or later
- A Ceph cluster with the mgr Prometheus module enabled (it is on by default in Reef and later)

Enable the Prometheus module if needed:
```
ceph mgr module enable prometheus
```

## Build from Source

```bash
git clone --recurse-submodules https://github.com/xorpaul/go-ceph-board.git
cd go-ceph-board
go build
```

Or use the included release script:
```bash
./go-build-release/build_release.sh v1.4.0
```

## Quick Start

```bash
# No config — plain HTTP, connects to localhost:9283
./go-ceph-board

# With config file
./go-ceph-board -config config.yaml
```

Open your browser to `http://localhost:8080`.

## Configuration

Copy `example.yaml` to `config.yaml` and adjust:

```yaml
server:
  address: ""      # empty = all interfaces
  port: "8080"

tls:
  enabled: false   # set true to require client certificates

ceph:
  mgr_prometheus_urls:
    - "http://ceph-mon1.example.com:9283"
  node_exporter_port: "9100"          # port node_exporter listens on (default 9100)
  node_exporter_host_suffix: ""       # optional suffix appended to hostnames when contacting node_exporter
```

### Stretch / multi-site clusters

For clusters where any monitor may be the active mgr, list all mgr Prometheus endpoints. The first URL that returns `ceph_health_status` is used:

```yaml
ceph:
  mgr_prometheus_urls:
    - "http://ceph-mon1.site1.example.com:9283"
    - "http://ceph-mon2.site1.example.com:9283"
    - "http://ceph-mon3.site1.example.com:9283"
    - "http://ceph-mon1.site2.example.com:9283"
    - "http://ceph-mon2.site2.example.com:9283"
    - "http://ceph-mon3.site2.example.com:9283"
```

### Multi-cluster configuration

To monitor multiple independent Ceph clusters, declare named sub-keys under `ceph:`. Each cluster carries its own endpoint list, node_exporter port, and host suffix. Global `insecure_skip_verify` and `ca_file` apply to all clusters:

```yaml
ceph:
  insecure_skip_verify: false
  EU:
    mgr_prometheus_urls:
      - "http://ceph-mon1.eu.example.com:9283"
      - "http://ceph-mon2.eu.example.com:9283"
    node_exporter_port: "9100"
  US:
    mgr_prometheus_urls:
      - "http://ceph-mon1.us.example.com:9283"
      - "http://ceph-mon2.us.example.com:9283"
    node_exporter_port: "9100"
    node_exporter_host_suffix: ".us.example.com"
```

The cluster switcher dropdown appears automatically when more than one cluster is configured. Cluster names starting with `EU` get a 🇪🇺 prefix and names starting with `US` get 🇺🇸.

### Pinned MDS hosts

By default the MDS Hosts table only shows hosts with at least one daemon currently in the Ceph MDS map. If a host goes fully down (all its MDS daemons fail over to standbys on other nodes), it disappears from `ceph_mds_metadata` and the table stops showing it — even though it should logically appear as down.

To keep such a host visible in the MDS Hosts table, list it explicitly under the cluster config:

```yaml
ceph:
  US:
    mgr_prometheus_urls:
      - "http://ceph-mon1.site1.example.com:9283"
    mds_hosts:
      - "mds1.site1"
      - "mds2.site1"
      - "mds3.site1"
```

Hosts in `mds_hosts` are always included in the node_exporter scrape regardless of Ceph metadata. If node_exporter is unreachable the row appears in the MDS table with a red "DOWN / unreachable" indicator. When the host recovers and its MDS daemons re-register with the cluster, it shows normally again with active/standby counts.

This field is optional. When omitted, only hosts currently visible in mgr metrics are shown.

### Extra hosts for node_exporter scraping

By default, `go-ceph-board` only scrapes node_exporter on OSD hosts and the hosts in `mds_hosts`. Admin or misc nodes that run textfile collectors (e.g. an `admin1` node) are not discovered automatically from Ceph metadata. Add them to the `extra_hosts` list so their metrics are included in the `/node-metrics` fan-out:

```yaml
ceph:
  EU:
    mgr_prometheus_urls:
      - "http://ceph-mon1.eu.example.com:9283"
    extra_hosts:
      - "admin1.site1.example.com"
      - "admin1.site2.example.com"
  US:
    node_exporter_host_suffix: ".us.example.com"
    extra_hosts:
      - "admin1.site3"   # suffix appended automatically → admin1.site3.us.example.com
```

Extra hosts are merged into the scrape list alongside the auto-discovered hosts (deduplication applied). The `node_exporter_host_suffix` is appended if the hostname does not already end with it, consistent with how OSD/MDS hostnames are handled. Scrape results from extra hosts are logged at INFO level showing duration and the number of `ceph_mds_rank_incarnation` lines found.

This field is optional. When omitted, only OSD/MDS hosts from Ceph metadata and `mds_hosts` are scraped.

### External links

Optional labeled buttons rendered in the dashboard header (top-right, left of the cluster switcher). Useful for linking to Grafana, Alertmanager, or any other internal tool. Each entry needs a `label` (button text) and a `url` (opened in a new tab):

```yaml
external_links:
  - label: "Grafana"
    url: "https://grafana.internal/d/ceph"
  - label: "Alertmanager"
    url: "https://alertmanager.internal"
```

The section is hidden entirely when the list is absent or empty.

### Host links

Optionally, every hostname in the OSD Hosts, MDS Hosts and Monitors tables gets a small 🖥️ link, e.g. to a CMDB or inventory search. `{host}` in the URL is replaced by the URL-encoded hostname:

```yaml
host_link:
  url: "https://cmdb.example.com/search?q={host}"
  title: "Search CMDB for"
```

Without `host_link` no icon is shown.

### TLS client certificate authentication

```yaml
tls:
  enabled: true
  ca_file:   "/etc/pki/ca.crt"
  cert_file: "/etc/pki/server.crt"
  key_file:  "/etc/pki/server.key"
  allowed_cns:
    - "alice"
    - "monitoring-service"
  health_check_port: "8081"   # optional: plain-HTTP port for HAProxy health checks
  cert_help_url: "https://pki.example.com/"            # optional: linked from the 401 page
  access_help_url: "https://tickets.example.com/new"   # optional: linked from the 403 page
```

Certificates are watched and reloaded automatically on change — no restart required.

## Command Line Options

```
./go-ceph-board [options]

  -config string   Path to YAML configuration file
  -debug           Enable debug logging
  -verbose         Enable verbose logging
  -version         Show version and build time
```

## Site Detection

The host tables group and colour rows by site. A host's site is the first entry of `sites` whose `match` is a substring (case-insensitive) of:

1. The `hostname` label on `ceph_osd_metadata` / `ceph_mds_metadata` / `ceph_mon_metadata`
2. CRUSH location labels (`datacenter`, `room`, `rack`) on the same metric

```yaml
sites:
  - {match: "dc1", label: "DC1"}                    # blue
  - {match: "dc2", label: "DC2"}                    # green
  - {match: "dc3", label: "DC3", color: "#f59e0b"}  # own colour (bg: darker variant, optional)
```

Without `color`/`bg`, colours come from a built-in palette by position (blue, green, amber, violet, pink, cyan). Hosts that match no site, or all hosts when `sites` is not set, show as `unknown`. Hosts whose short hostname has no site marker (e.g. `mds1` next to `mds1.dc1`) inherit the site of a peer with the same IP.

## MDS Rank Assignment Tracking

go-ceph-board can record the exact time each MDS daemon took a rank (failover or restart into a rank slot). This is surfaced in the MDS daemon detail grid as a "Since" column showing wall-clock age rather than just the systemd service start time.

### How it works

The Ceph mgr Prometheus exporter does not expose per-rank assignment timestamps. Instead, go-ceph-board watches the `ceph_mds_rank_incarnation` counter emitted by a textfile helper script running on admin/misc nodes. Every time the incarnation counter for a `(rank, fs_id)` pair changes, go-ceph-board records the current timestamp in `rank_assignments.json` and serves it from `/rank-assignments`.

The frontend reads `/rank-assignments` on each refresh. For each daemon in the detail grid, it uses the `assigned_at` timestamp from that response as the "Since" value, falling back to `ceph_daemon_start_time_seconds` when the rank-assignment data is unavailable. Rows where "Since" is under 1 hour are highlighted red as a visual indicator of a recent failover.

### Deploying the textfile helper

A helper script `helper_scripts/ceph_mdsmap_textfile.sh` must run on a node that can read `ceph fs dump`, typically an admin node in each site.

The script auto-detects the node_exporter textfile directory from the running node_exporter's `--collector.textfile.directory` cmdline flag, falling back to `/var/lib/prometheus/node-exporter`. It emits four metric families:

| Metric | Description |
|--------|-------------|
| `ceph_mds_rank_incarnation` | Incarnation counter per rank slot — the key signal for rank reassignment |
| `ceph_mds_rank_state_seq` | State sequence number per rank slot |
| `ceph_mds_rank_assigned` | 1 when the rank is assigned to a daemon, 0 for unassigned |
| `ceph_mdsmap_epoch` | Current MDS map epoch |

Run the script from cron or a systemd timer every few minutes. That host must be listed under `extra_hosts` in the go-ceph-board cluster config (see above) so its node_exporter output is included in the scrape fan-out.

### Persisted state

Rank assignments are persisted to `rank_assignments.json` in the working directory on each change and reloaded at startup. The file is a JSON object keyed by daemon name:

```json
{
  "EU": {
    "fs7.dc1.mds1.xswazx": {
      "rank": 0,
      "fs_id": "7",
      "daemon": "fs7.dc1.mds1.xswazx",
      "incarnation": 747313,
      "assigned_at": 1724853600
    }
  }
}
```

## Architecture

- **Backend**: Go HTTP server with four metric endpoints:
  - `GET /clusters` — returns cluster list, default cluster, per-cluster Ceph versions, and the configured `external_links`, `sites` and `host_link` for the frontend
  - `GET /ceph-metrics?cluster=<name>` — proxies to the active mgr Prometheus endpoint, detected by response content with fallback across the configured URL list; defaults to the first configured cluster
  - `GET /node-metrics?cluster=<name>` — auto-discovers OSD/MDS hostnames from the cached mgr response, fans out to each host's node_exporter (plus any configured `extra_hosts`) with a 3-second TCP pre-dial (fast failure for down hosts) and a 30-second per-host fetch timeout; filters to CPU/memory/mdsmap metric families and returns a merged Prometheus body with `instance` labels injected; rank-assignment metrics from extra hosts are fed into the rank tracker on each cycle
  - `GET /rank-assignments?cluster=<name>` — returns the persisted rank-assignment map for the cluster: `{daemon: {rank, fs_id, incarnation, assigned_at}}`. Populated from `ceph_mds_rank_incarnation` data arriving via extra-host node_exporter scrapes; backed by `rank_assignments.json` on disk
- **Server-side cache**: both metric endpoints share a 10-second TTL cache per cluster; concurrent dashboard clients coalesce into one upstream fetch per TTL window
- **Cache warmup**: on startup, mgr and node metrics are pre-fetched in parallel for all configured clusters so the first browser request is served from cache
- **Frontend**: browser-side JavaScript fetches both endpoints in parallel (`Promise.allSettled`); a node-metrics failure is non-fatal and shows `—` in affected columns; switching clusters tears down all history and chart state
- **Static assets**: tailwind.css, chart.js, chartjs-adapter-date-fns — all embedded in the binary
- **Auth**: TLS client cert on the board's own listener; Ceph and node_exporter sides need no credentials

## Dependencies

- [`github.com/fsnotify/fsnotify`](https://github.com/fsnotify/fsnotify) — certificate file watching
- [`gopkg.in/yaml.v3`](https://pkg.go.dev/gopkg.in/yaml.v3) — config parsing
- [Chart.js](https://www.chartjs.org/) — charts (embedded)
- [Tailwind CSS](https://tailwindcss.com/) — styling (embedded)

## Troubleshooting

**Dashboard shows "Connection failed"**
Check that the mgr Prometheus endpoint is reachable from the host running go-ceph-board:
```bash
curl http://ceph-mon1.example.com:9283/metrics | head -20
```
If it times out, the mgr module may not be running on that mon — try another mon host, or check `ceph mgr stat`.

**OSD host table is empty / all hosts show as "unknown" site**
The `ceph_osd_metadata` metric must be present in the Prometheus output. Verify with:
```bash
curl -s http://ceph-mon1.example.com:9283/metrics | grep ceph_osd_metadata | head -3
```
If the `hostname` label doesn't contain one of your `sites` matches, add a matching entry (see [Site Detection](#site-detection)) or ensure your CRUSH map has `datacenter` or `room` labels set.

**MDS Host Memory shows "—" for all active daemons**
The mgr may be running with `exclude_perf_counters = true`, which suppresses `ceph_mds_mem_rss`. The dashboard shows an amber banner explaining this. Re-enable with:
```bash
ceph config set mgr mgr/prometheus/exclude_perf_counters false
```

**node_exporter data missing for some hosts**
If hostnames in mgr metadata are short (e.g. `mds1.dc1`) but node_exporter listens on an FQDN, set `node_exporter_host_suffix` in the cluster config:
```yaml
node_exporter_host_suffix: ".example.com"
```
node_exporter fetch activity (success/failure per host) is logged at INFO level — check `journalctl` without needing to restart with `-debug`.

**Pool IOPS show 0 on first load**
IOPS are calculated as the delta between two successive Prometheus scrapes. They show 0 until the second refresh cycle completes.

**`/rank-assignments` always returns `{}`**
Two things must be in place:

1. The `ceph_mdsmap_textfile.sh` script must be running on an admin/mon node and writing `ceph_mdsmap.prom` to the node_exporter textfile directory (e.g. `ls -la /var/lib/prometheus/node-exporter/`).
2. That node must be listed under `extra_hosts` in the cluster config so go-ceph-board includes it in the node_exporter scrape fan-out.

Check the journal for lines like `cluster EU: extra_host admin1.dc1.example.com:9100: ok (142ms): 109 ceph_mds_rank_incarnation lines` — these confirm the metrics are being picked up. If you see the host listed but 0 lines, confirm the `.prom` file exists and contains `ceph_mds_rank_incarnation` lines.

**Debug logging**
```bash
./go-ceph-board -debug -config config.yaml
```
