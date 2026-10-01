# go-ceph-board

Real-time Ceph cluster dashboard. Single Go binary, no runtime dependencies — all static assets embedded via `embed.FS`.

## Build & run

```bash
go build -o go-ceph-board .
./go-ceph-board                        # plain HTTP on :8080, no config
./go-ceph-board -config config.yaml   # TLS + custom endpoints
./go-ceph-board -config config.yaml -debug
```

## Live cluster config

A git-ignored `./config.yaml` may exist in the repo with real cluster endpoints. If present, use it when developing or testing against a live cluster — run `./go-ceph-board -config config.yaml` and the dashboard will connect to the real Ceph deployment.

The dashboard tries each `mgr_prometheus_urls` entry in order and uses the first that returns `ceph_health_status` (i.e. the active mgr). From the mgr `/metrics` response it extracts `ceph_osd_metadata` and `ceph_mds_metadata` labels to discover all OSD and MDS hostnames — the entire live cluster topology is derived from that single endpoint.

## Architecture

Two backend endpoints, both served from a 10-second server-side cache:

- **`/ceph-metrics`** — proxies the active mgr Prometheus endpoint
- **`/node-metrics`** — discovers OSD/MDS hosts from the cached mgr metrics, fans out concurrent HTTP fetches to each host's node_exporter (default port 9100), filters to the metrics the dashboard needs, and returns the aggregated result with per-host `instance=` labels

The dashboard JS fetches both in parallel on each refresh tick. Node metrics failure is non-fatal — CPU/mem/uptime/OS columns show `—` if node_exporter is unreachable.

## Key config fields

| field | default | purpose |
|---|---|---|
| `ceph.mgr_prometheus_urls` | — | list of mgr endpoints, first active wins |
| `ceph.node_exporter_port` | `9100` | port for per-host node_exporter scrapes |
| `ceph.node_exporter_host_suffix` | `""` | appended to hostnames discovered from mgr metrics (e.g. `.internal`) |
| `ceph.insecure_skip_verify` | `false` | skip TLS verification for mgr endpoint |

## Code layout

- `go-ceph-board.go` — HTTP server, config, caching, mgr/node metric fetching
- `html.go` — entire dashboard as an embedded HTML/JS string (`dashboardHTML`); `dashboardHandler` serves it
- `static/` — embedded JS libs (Chart.js, Tailwind, date-fns adapter)
