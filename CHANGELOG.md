# Changelog

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
