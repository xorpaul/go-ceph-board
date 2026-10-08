# Changelog

## v1.5.1 (2026-10-08)

### Fixed
- **MDS role classification from `ceph_mds_rank_assigned` never matched** — the textfile metric labels daemons with the bare name from `ceph fs dump` (`fs1.site2.mds1.abcdef`), while the mgr Prometheus metrics use `mds.fs1.site2.mds1.abcdef`, so every lookup in the v1.5.0 state map missed. With the helper deployed, the MDS Hosts table and memory charts counted every standby-replay as active, and the MDS Daemons table still listed every daemon as standby (the v1.5.0 fix had no effect). `buildDaemonRankStateMap` now keys by the mgr form (adding `mds.` and stripping a cephadm FSID prefix), so all rank-holding daemons resolve.
- **Per-filesystem daemon grid used CAPS to tell active from standby-replay** — an idle active rank (0 client caps) was shown as a standby-replay: no Jrnl/Lag values, missing from the ∑ Jrnl total, and it could hide a real "no standby-replay" placeholder. The grid, the rank-0 site strip and the modal daemon list now use the `ceph_mds_rank_assigned` state, falling back to CAPS > 0 when the helper is not deployed.
- **SR journal lag colours** — with the live journal size unknown, an SR margin is now red below 50 MB instead of always green. Lag thresholds are 1 MiB / 10 MiB, matching the binary units displayed (previously 1e6 / 10e6).
- **Duplicate "no standby-replay" rows** — a rank briefly held by two daemons during failover no longer produces two placeholder rows.
- **SR lag trend history** is pruned for daemons that are no longer reported, so redeploys (cephadm names carry random suffixes) no longer grow it forever. It is kept when the lag metric is missing entirely.

### Added
- **`helper_scripts/ceph_mds_sr_lag_textfile.py`** — the textfile helper that produces `ceph_mds_sr_lag_bytes`, `ceph_mds_sr_margin_bytes`, `ceph_mds_journal_live_bytes` and `ceph_mds_sr_present` for the Jrnl and Lag columns, which previously had no public source and always showed `—`. Documented in the README under "Standby-Replay Journal Lag".
- The totals-row Lag tooltip shows the smallest SR margin value next to the daemon name.

## v1.5.0 (2026-10-05)

### Fixed
- **MDS daemon role classification on idle cephadm clusters** — `ceph_mds_metadata.state` is always empty in cephadm deployments, so all MDS daemons were shown as "Standby / Passive" even when active or in standby-replay. The dashboard now reads `ceph_mds_rank_assigned` from the textfile metric emitted by `helper_scripts/ceph_mdsmap_textfile.sh` on misc/mon hosts (added to `extra_hosts` in config) and uses that as the authoritative state source. Falls back to the previous activity-based heuristic when the textfile helper is not deployed.

## v1.4.0 (2026-10-01)

First public release. The README lists the full feature set: cluster health, PG/OSD/usage charts, pool, OSD host, monitor and MDS tables, MDS session map and rank-assignment tracking, multi-cluster switching, configurable Prometheus graphs, and TLS client-certificate auth.

### Added
- **`sites` config** — a list of `{match, label, color, bg}` entries that maps hostname or CRUSH-label substrings to the site shown and coloured in the host tables. Colours default to a palette by position. Replaces the site names that were built into the dashboard.
- **`host_link` config** — an optional 🖥️ link next to every hostname in the OSD Hosts, MDS Hosts and Monitors tables, e.g. to a CMDB search; `{host}` in the URL is replaced by the hostname. Replaces the built-in CMDB link.
- **`tls.cert_help_url` and `tls.access_help_url`** — optional links on the 401 (no client certificate) and 403 (CN not allowed) pages. Replaces the built-in PKI and ticket links.

### Changed
- The 403 page HTML-escapes the rejected CN.
- `/clusters` also returns `sites` and `host_link` for the frontend.
- `go-build-release` is a git submodule again.
