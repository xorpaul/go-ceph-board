package main

const htmlJSMetrics = `

// ─── Pool rate tracking ───────────────────────────────────────────────────────
// Rolling window: keep last RATE_WINDOW samples per pool and compute rate
// oldest-to-newest. This prevents 0-rate flicker when two consecutive polls
// land on the same MGR counter snapshot (MGR updates on its own ~5-15s cadence).
const RATE_WINDOW = 5;
const poolCounterHistory = {}; // pool_id -> [{rd, wr, rdBytes, wrBytes, ts}, ...]

function calcPoolRates(poolId, rd, wr, rdBytes, wrBytes) {
    const now = Date.now();
    if (!poolCounterHistory[poolId]) poolCounterHistory[poolId] = [];
    const hist = poolCounterHistory[poolId];
    hist.push({ rd, wr, rdBytes, wrBytes, ts: now });
    if (hist.length > RATE_WINDOW) hist.shift();
    if (hist.length < 2) return { rdIops: 0, wrIops: 0, rdMBps: 0, wrMBps: 0 };
    const oldest = hist[0], newest = hist[hist.length - 1];
    const elapsed = (newest.ts - oldest.ts) / 1000;
    if (elapsed <= 0) return { rdIops: 0, wrIops: 0, rdMBps: 0, wrMBps: 0 };
    return {
        rdIops: Math.max(0, Math.round((newest.rd      - oldest.rd)      / elapsed)),
        wrIops: Math.max(0, Math.round((newest.wr      - oldest.wr)      / elapsed)),
        rdMBps: Math.max(0, (newest.rdBytes - oldest.rdBytes) / elapsed / 1048576),
        wrMBps: Math.max(0, (newest.wrBytes - oldest.wrBytes) / elapsed / 1048576),
    };
}

// ─── MDS daemon rate tracking ─────────────────────────────────────────────────
// Mirrors the pool rate approach: rolling window per daemon, Math.max(0,...)
// guards against counter resets when a daemon restarts.
const MDS_RATE_WINDOW = 5;
const mdsCounterHistory = {}; // ceph_daemon -> [{slowReply, request, ts}, ...]

function calcMdsDaemonRates(cephDaemon, slowReply, request) {
    const now = Date.now();
    if (!mdsCounterHistory[cephDaemon]) mdsCounterHistory[cephDaemon] = [];
    const hist = mdsCounterHistory[cephDaemon];
    // Skip duplicate calls within the same refresh cycle (same counters, called <500 ms apart).
    // calcMdsDaemonRates is called up to 3× per daemon per refresh; without this guard the
    // rolling window fills with near-identical entries and the ∑ row diverges from per-row values.
    const last = hist[hist.length - 1];
    if (!last || last.slowReply !== slowReply || last.request !== request || (now - last.ts) >= 500) {
        hist.push({ slowReply, request, ts: now });
        if (hist.length > MDS_RATE_WINDOW) hist.shift();
    }
    if (hist.length < 2) return { slowReplyRate: 0, reqRate: 0 };
    const oldest = hist[0], newest = hist[hist.length - 1];
    const elapsed = (newest.ts - oldest.ts) / 1000;
    if (elapsed <= 0) return { slowReplyRate: 0, reqRate: 0 };
    return {
        slowReplyRate: Math.max(0, (newest.slowReply - oldest.slowReply) / elapsed),
        reqRate:       Math.max(0, (newest.request   - oldest.request)   / elapsed),
    };
}

// ─── MDS trim rate tracking ───────────────────────────────────────────────────
// Rolling window per daemon: same duplicate-call guard as calcMdsDaemonRates.
// Returns null on first poll (no baseline), otherwise inodes expired per second.
const mdsTrimHistory = {}; // ceph_daemon -> [{expired, ts}, ...]

function calcMdsTrimRate(cephDaemon, expired) {
    const now = Date.now();
    if (!mdsTrimHistory[cephDaemon]) mdsTrimHistory[cephDaemon] = [];
    const hist = mdsTrimHistory[cephDaemon];
    const last = hist[hist.length - 1];
    if (!last || last.expired !== expired || (now - last.ts) >= 500) {
        hist.push({ expired, ts: now });
        if (hist.length > MDS_RATE_WINDOW) hist.shift();
    }
    if (hist.length < 2) return null;
    const oldest = hist[0], newest = hist[hist.length - 1];
    const elapsed = (newest.ts - oldest.ts) / 1000;
    if (elapsed <= 0) return null;
    return Math.max(0, (newest.expired - oldest.expired) / elapsed);
}

// ─── MDS cap recall rate tracking ────────────────────────────────────────────
// Rolling window per daemon tracking ceph_mds_ceph_cap_op_revoke (cumulative counter).
// Returns null on first poll (no baseline), otherwise cap revokes per second.
const mdsRecallHistory = {}; // ceph_daemon -> [{revoke, ts}, ...]

function calcMdsRecallRate(cephDaemon, revoke) {
    const now = Date.now();
    if (!mdsRecallHistory[cephDaemon]) mdsRecallHistory[cephDaemon] = [];
    const hist = mdsRecallHistory[cephDaemon];
    const last = hist[hist.length - 1];
    if (!last || last.revoke !== revoke || (now - last.ts) >= 500) {
        hist.push({ revoke, ts: now });
        if (hist.length > MDS_RATE_WINDOW) hist.shift();
    }
    if (hist.length < 2) return null;
    const oldest = hist[0], newest = hist[hist.length - 1];
    const elapsed = (newest.ts - oldest.ts) / 1000;
    if (elapsed <= 0) return null;
    return Math.max(0, (newest.revoke - oldest.revoke) / elapsed);
}

// ─── Host CPU rolling-window rate ────────────────────────────────────────────
// Tracks (idleSum, totalSum, ts) per hostname. Two consecutive samples are
// needed before a percentage can be returned (returns null on the first poll).
const HOST_CPU_WINDOW = 5;
const hostCpuHistory = {}; // hostname -> [{idle, total, ts}, ...]

function calcHostCpuPct(hostname, idleSum, totalSum) {
    const now = Date.now();
    if (!hostCpuHistory[hostname]) hostCpuHistory[hostname] = [];
    const hist = hostCpuHistory[hostname];
    hist.push({ idle: idleSum, total: totalSum, ts: now });
    if (hist.length > HOST_CPU_WINDOW) hist.shift();
    if (hist.length < 2) return null;
    const oldest = hist[0], newest = hist[hist.length - 1];
    const dTotal = newest.total - oldest.total;
    const dIdle  = newest.idle  - oldest.idle;
    if (dTotal <= 0) return null;
    return Math.max(0, Math.min(100, (1 - dIdle / dTotal) * 100));
}

function buildHostMetricsMap(nodeMetrics) {
    // Collect all known instances from node_cpu_seconds_total
    const instances = new Set();
    for (const e of (nodeMetrics['node_cpu_seconds_total'] || [])) {
        if (e.labels.instance) instances.add(e.labels.instance);
    }
    // Also include hosts that have memory metrics but no CPU entries yet
    for (const e of (nodeMetrics['node_memory_MemTotal_bytes'] || [])) {
        if (e.labels.instance) instances.add(e.labels.instance);
    }

    const map = {};
    for (const hostname of instances) {
        // Sum all CPU seconds across every core; count distinct cpu numbers
        let idleSum = 0, totalSum = 0;
        const cpuNums = new Set();
        for (const e of (nodeMetrics['node_cpu_seconds_total'] || [])) {
            if (e.labels.instance !== hostname) continue;
            totalSum += e.value;
            if (e.labels.mode === 'idle') idleSum += e.value;
            if (e.labels.cpu !== undefined) cpuNums.add(e.labels.cpu);
        }
        const hasCpu = totalSum > 0;
        const cpuPct   = hasCpu ? calcHostCpuPct(hostname, idleSum, totalSum) : null;
        const cpuCount = cpuNums.size > 0 ? cpuNums.size : undefined;

        const cpuInfoE = (nodeMetrics['node_cpu_info'] || []).find(e => e.labels.instance === hostname);
        const cpuModel = cpuInfoE ? (cpuInfoE.labels.model_name || null) : null;

        const memTotalE = (nodeMetrics['node_memory_MemTotal_bytes'] || []).find(e => e.labels.instance === hostname);
        const memAvailE = (nodeMetrics['node_memory_MemAvailable_bytes'] || []).find(e => e.labels.instance === hostname);
        const memTotal  = memTotalE ? memTotalE.value : 0;
        const memAvail  = memAvailE ? memAvailE.value : 0;
        const memPct    = memTotal > 0 ? (memTotal - memAvail) / memTotal * 100 : null;

        const bootTimeE = (nodeMetrics['node_boot_time_seconds'] || []).find(e => e.labels.instance === hostname);
        const bootTime  = bootTimeE ? bootTimeE.value : null;

        const osInfoE  = (nodeMetrics['node_os_info']   || []).find(e => e.labels.instance === hostname);
        const unameE   = (nodeMetrics['node_uname_info'] || []).find(e => e.labels.instance === hostname);
        let osName = null;
        if (osInfoE && osInfoE.labels.pretty_name) {
            osName = osInfoE.labels.pretty_name;
        } else if (unameE) {
            osName = (unameE.labels.sysname || 'Linux') + ' ' + (unameE.labels.release || '');
        }

        map[hostname] = { cpuPct, cpuCount, cpuModel, memPct, memTotal, memAvail, bootTime, osName };
    }
    // Mark hosts that were discovered but could not be reached by node_exporter.
    for (const e of (nodeMetrics['go_ceph_node_unreachable'] || [])) {
        if (!e.labels.instance) continue;
        if (!map[e.labels.instance]) map[e.labels.instance] = {};
        map[e.labels.instance].unreachable = true;
    }
    // Mark hosts explicitly declared as MDS hosts in config — used by the MDS
    // Hosts table to show a row even when the host has fallen off ceph_mds_metadata.
    for (const e of (nodeMetrics['go_ceph_configured_mds_host'] || [])) {
        if (!e.labels.instance) continue;
        if (!map[e.labels.instance]) map[e.labels.instance] = {};
        map[e.labels.instance].configuredMDS = true;
    }
    // Add short-name aliases from go_ceph_host_canonical (emitted when Go resolves a
    // bare short hostname to an FQDN via IP-peer dedup or PTR lookup).  This lets the
    // OSD host table look up node metrics by the raw ceph_osd_metadata hostname even
    // when the running daemon was registered under the old short name and hasn't been
    // restarted since a hostname change.  The _canonical field carries the FQDN so
    // callers can also derive the correct site label from the resolved hostname.
    for (const e of (nodeMetrics['go_ceph_host_canonical'] || [])) {
        const short = e.labels.instance;
        const canonical = e.labels.canonical;
        if (!short || !canonical || short === canonical) continue;
        if (!map[short]) {
            const base = map[canonical] || {};
            map[short] = Object.assign({}, base, { _canonical: canonical });
        }
    }
    return map;
}

// buildDaemonMemMap returns {ceph_daemon -> bytes} from the ceph_daemon_memory_bytes
// textfile metric written by the helper script on each host.
function buildDaemonMemMap(nodeMetrics) {
    const map = {};
    for (const m of (nodeMetrics['ceph_daemon_memory_bytes'] || [])) {
        if (m.labels.ceph_daemon) map[m.labels.ceph_daemon] = m.value;
    }
    return map;
}

// buildDaemonStartTimeMap returns {ceph_daemon -> epochSeconds} from the
// ceph_daemon_start_time_seconds textfile metric written by the helper script.
// buildDaemonMemLimitMap returns {ceph_daemon -> bytes} from the
// ceph_mds_cache_memory_limit_bytes textfile metric written by ceph-mds-client-caps-metrics.py.
// Keys use the full "mds.<name>" form matching d.cephDaemon from mgr Prometheus.
function buildDaemonMemLimitMap(nodeMetrics) {
    const map = {};
    for (const m of (nodeMetrics['ceph_mds_cache_memory_limit_bytes'] || [])) {
        if (m.labels.ceph_daemon) map[m.labels.ceph_daemon] = m.value;
    }
    return map;
}

// buildSrLagMap returns {ceph_daemon (SR) -> {lag, margin}} from node metrics.
// When both misc hosts report the same cluster, each series arrives twice;
// keep the most conservative values: max lag, min margin.
function buildSrLagMap(nodeMetrics) {
    const map = {};
    for (const m of (nodeMetrics['ceph_mds_sr_lag_bytes'] || [])) {
        const k = m.labels.ceph_daemon;
        if (!k) continue;
        const e = map[k] || (map[k] = {});
        e.lag = e.lag === undefined ? m.value : Math.max(e.lag, m.value);
    }
    for (const m of (nodeMetrics['ceph_mds_sr_margin_bytes'] || [])) {
        const k = m.labels.ceph_daemon;
        if (!k) continue;
        const e = map[k] || (map[k] = {});
        e.margin = e.margin === undefined ? m.value : Math.min(e.margin, m.value);
    }
    return map;
}

// buildJournalLiveMap returns {"fs_name/rank" -> bytes} for the untrimmed journal size.
// Duplicates: take max (fresher active expos = smaller live; max = more conservative).
function buildJournalLiveMap(nodeMetrics) {
    const map = {};
    for (const m of (nodeMetrics['ceph_mds_journal_live_bytes'] || [])) {
        if (!m.labels.fs_name || m.labels.rank === undefined) continue;
        const k = m.labels.fs_name + '/' + m.labels.rank;
        map[k] = map[k] === undefined ? m.value : Math.max(map[k], m.value);
    }
    return map;
}

// buildSrPresentMap returns {"fs_name/rank" -> 0|1}. Treat only === 0 as "no SR".
// When both misc hosts report, take min so a transient 0 is not hidden.
function buildSrPresentMap(nodeMetrics) {
    const map = {};
    for (const m of (nodeMetrics['ceph_mds_sr_present'] || [])) {
        if (!m.labels.fs_name || m.labels.rank === undefined) continue;
        const k = m.labels.fs_name + '/' + m.labels.rank;
        map[k] = map[k] === undefined ? m.value : Math.min(map[k], m.value);
    }
    return map;
}

// ─── SR lag trend tracking ────────────────────────────────────────────────────
// Tracks distinct lag samples per daemon (only pushed when value changes).
// Returns true when the lag grew in each of the last 3 distinct changes.
const mdsSrLagHistory = {}; // cephDaemon -> [bytes, ...]

function checkSrLagTrend(cephDaemon, lagBytes) {
    if (!mdsSrLagHistory[cephDaemon]) mdsSrLagHistory[cephDaemon] = [];
    const hist = mdsSrLagHistory[cephDaemon];
    if (hist.length === 0 || hist[hist.length - 1] !== lagBytes) {
        hist.push(lagBytes);
        if (hist.length > 10) hist.shift();
    }
    if (hist.length < 4) return false;
    const n = hist.length;
    return hist[n-1] > hist[n-2] && hist[n-2] > hist[n-3] && hist[n-3] > hist[n-4];
}

function buildDaemonStartTimeMap(nodeMetrics) {
    const map = {};
    // EU clusters prefix the ceph_daemon label with a cluster FSID UUID:
    //   "<uuid>.mds.fs1.site2.mds1.edmmur"  →  we want "mds.fs1.site2.mds1.edmmur"
    // Strip any leading "<uuid>." so the key matches d.cephDaemon from mgr prometheus.
    const uuidPrefix = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\./;
    for (const m of (nodeMetrics['ceph_daemon_start_time_seconds'] || [])) {
        if (!m.labels.ceph_daemon) continue;
        const key = m.labels.ceph_daemon.replace(uuidPrefix, '');
        map[key] = m.value;
    }
    return map;
}

// buildDaemonRankStateMap returns ceph_daemon → normalized state from
// ceph_mds_rank_assigned (textfile metric, via extra_hosts scrape).
// The textfile uses the "up:" prefix (e.g. "up:active", "up:standby-replay");
// we strip it so callers can compare against plain "active" / "standby-replay".
function buildDaemonRankStateMap(nodeMetrics) {
    const map = {};
    for (const m of (nodeMetrics['ceph_mds_rank_assigned'] || [])) {
        const daemon = m.labels.ceph_daemon;
        const state  = m.labels.state;
        if (!daemon || !state) continue;
        map[daemon] = state.startsWith('up:') ? state.slice(3) : state;
    }
    return map;
}

function cpuColor(pct) {
    if (pct >= 90) return '#ef4444';
    if (pct >= 75) return '#f97316';
    if (pct >= 50) return '#eab308';
    return '#22c55e';
}

function pctBarCellHtml(pct, colorFn, tooltip, subtext) {
    if (pct === null) return '<span class="text-xs text-gray-400 dark:text-gray-500">—</span>';
    const color = colorFn(pct);
    const tip = tooltip ? ' title="' + tooltip.replace(/&/g,'&amp;').replace(/"/g,'&quot;') + '"' : '';
    const pctSpan = '<span class="text-xs font-mono font-bold" style="color:' + color + '">' + pct.toFixed(0) + '%</span>';
    const inner = subtext
        ? '<div>' + pctSpan + '<br><span style="font-size:0.6rem" class="text-gray-400 dark:text-gray-500 font-mono">' + subtext + '</span></div>'
        : pctSpan;
    return '<div class="flex items-center gap-1"' + tip + '>' +
        '<div class="disk-bar-bg" style="width:48px"><div class="disk-bar-fill" style="width:' + Math.min(pct,100).toFixed(0) + '%;background:' + color + '"></div></div>' +
        inner +
        '</div>';
}

function formatUptimeHtml(bootTimeSecs, titleOverride) {
    if (bootTimeSecs === null || bootTimeSecs === undefined) return '<span class="text-xs text-gray-400 dark:text-gray-500">—</span>';
    const secs = Math.floor(Date.now() / 1000 - bootTimeSecs);
    if (secs < 0) return '<span class="text-xs text-gray-400 dark:text-gray-500">—</span>';
    const d = Math.floor(secs / 86400);
    const h = Math.floor((secs % 86400) / 3600);
    const m = Math.floor((secs % 3600) / 60);
    let str;
    if (d > 0)      str = d + 'd ' + h + 'h';
    else if (h > 0) str = h + 'h ' + m + 'm';
    else            str = m + 'm';
    const title = titleOverride ? ' title="' + titleOverride.replace(/"/g, '&quot;') + '"' : '';
    if (secs < 3600)
        return '<span class="text-xs font-mono font-bold" style="color:#ef4444"' + title + '>' + str + '</span>';
    return '<span class="text-xs font-mono text-gray-600 dark:text-gray-300"' + title + '>' + str + '</span>';
}

function formatDuration(secs) {
    if (secs === undefined || secs === null || isNaN(secs)) return '—';
    secs = Math.floor(secs);
    const d = Math.floor(secs / 86400);
    const h = Math.floor((secs % 86400) / 3600);
    const m = Math.floor((secs % 3600) / 60);
    const s = secs % 60;
    if (d > 0) return d + 'd ' + h + 'h';
    if (h > 0) return h + 'h ' + m + 'm';
    if (m > 0) return m + 'm ' + s + 's';
    return s + 's';
}

function shortenOSName(osName) {
    if (!osName) return osName;
    const rules = [
        { re: /^Red Hat Enterprise Linux(?: Server)? (\d+(?:\.\d+)?)/, to: 'RH$1' },
        { re: /^CentOS Linux (\d+(?:\.\d+)?)/, to: 'C$1' },
        { re: /^Debian GNU\/Linux (\d+)/, to: 'D$1' },
        { re: /^Rocky Linux (\d+(?:\.\d+)?)/, to: 'RK$1' },
        { re: /^AlmaLinux (\d+(?:\.\d+)?)/, to: 'AL$1' },
        { re: /^Ubuntu (\d+\.\d+)/, to: 'U$1' },
        { re: /^SUSE Linux Enterprise Server (\d+)/, to: 'SLES$1' },
        { re: /^openSUSE Leap (\d+(?:\.\d+)?)/, to: 'oS$1' },
        { re: /^Oracle Linux(?:(?: Server)?) (\d+(?:\.\d+)?)/, to: 'OL$1' },
        { re: /^Amazon Linux (\d+)/, to: 'AMZ$1' },
    ];
    for (const r of rules) {
        const m = osName.match(r.re);
        if (m) return r.to.replace('$1', m[1]);
    }
    // Generic fallback: strip parenthesised codename suffix and GNU/Linux prefix
    return osName.replace(/ \(.*?\)$/, '').replace(/GNU\/Linux /, '');
}

function getOSColor(osName) {
    if (!osName) return { bg: '#6b7280', text: '#ffffff' };
    // Hash the full string so each distinct version string gets a stable, unique slot
    let hash = 0;
    for (let i = 0; i < osName.length; i++) hash = osName.charCodeAt(i) + ((hash << 5) - hash);
    const palette = [
        { bg: '#059669', text: '#ffffff' }, // emerald
        { bg: '#2563eb', text: '#ffffff' }, // blue
        { bg: '#7c3aed', text: '#ffffff' }, // violet
        { bg: '#dc2626', text: '#ffffff' }, // red
        { bg: '#f59e0b', text: '#ffffff' }, // amber
        { bg: '#ea580c', text: '#ffffff' }, // orange
        { bg: '#be123c', text: '#ffffff' }, // rose
        { bg: '#374151', text: '#ffffff' }, // slate
        { bg: '#0891b2', text: '#ffffff' }, // cyan
        { bg: '#65a30d', text: '#ffffff' }, // lime
        { bg: '#c026d3', text: '#ffffff' }, // fuchsia
        { bg: '#0d9488', text: '#ffffff' }, // teal
    ];
    return palette[Math.abs(hash) % palette.length];
}

function formatOsHtml(osName) {
    if (!osName) return '<span class="text-xs text-gray-400 dark:text-gray-500">—</span>';
    const short = shortenOSName(osName);
    const c = getOSColor(osName);
    return '<span style="background:' + c.bg + ';color:' + c.text + ';padding:1px 5px;border-radius:3px;font-family:monospace;font-size:10px;font-weight:bold" title="' + osName.replace(/"/g, '&quot;') + '">' + short + '</span>';
}

// ─── Dashboard update functions ───────────────────────────────────────────────

function updateHealth(metrics) {
    const status = scalar(metrics, 'ceph_health_status', -1);
    const el = document.getElementById('healthStatus');
    const dot = document.getElementById('healthDot');
    if (status === 0) {
        el.textContent = 'HEALTH_OK';
        el.style.color = '#22c55e';
        dot.className = 'status-dot bg-green-500';
    } else if (status === 1) {
        el.textContent = 'HEALTH_WARN';
        el.style.color = '#eab308';
        dot.className = 'status-dot bg-yellow-500';
    } else if (status === 2) {
        el.textContent = 'HEALTH_ERR';
        el.style.color = '#ef4444';
        dot.className = 'status-dot bg-red-500';
    } else {
        el.textContent = '-';
        dot.className = 'status-dot bg-gray-400';
    }
}

// inferHealthChecks derives active check descriptions from Prometheus metrics.
// Returns [] when health is OK or no relevant metric is non-zero.
// Covers: OSD down/out, monitor quorum loss, PG degraded/undersized/recovering/backfilling.
// Does NOT cover: SLOW_OPS, clock skew, scrub warnings, or any check not expressed as a metric.
function inferHealthChecks(metrics) {
    const checks = [];

    // OSD state: cross-reference up and in by daemon name
    const upArr = metrics['ceph_osd_up'] || [];
    const inArr = metrics['ceph_osd_in'] || [];
    const inByDaemon = {};
    for (const e of inArr) if (e.labels.ceph_daemon) inByDaemon[e.labels.ceph_daemon] = e.value;
    let downIn = 0, upOut = 0;
    for (const e of upArr) {
        const iv = inByDaemon[e.labels.ceph_daemon];
        if (e.value === 0 && iv === 1) downIn++;
        else if (e.value === 1 && iv === 0) upOut++;
    }
    if (downIn > 0) checks.push({ sev: 'ERR',  msg: downIn + ' OSD' + (downIn > 1 ? 's' : '') + ' down' });
    if (upOut  > 0) checks.push({ sev: 'WARN', msg: upOut  + ' OSD' + (upOut  > 1 ? 's' : '') + ' up but out' });

    // Monitor quorum
    const downMons = (metrics['ceph_mon_quorum_status'] || []).filter(e => e.value === 0).length;
    if (downMons > 0) checks.push({ sev: 'ERR', msg: downMons + ' monitor' + (downMons > 1 ? 's' : '') + ' not in quorum' });

    // PG states
    const pgDeg  = sumVec(metrics, 'ceph_pg_degraded',    0);
    const pgUs   = sumVec(metrics, 'ceph_pg_undersized',   0);
    const pgRec  = sumVec(metrics, 'ceph_pg_recovering',   0);
    const pgBf   = sumVec(metrics, 'ceph_pg_backfilling',  0);
    const pgBfW  = sumVec(metrics, 'ceph_pg_backfill_wait',0);
    if (pgDeg > 0) checks.push({ sev: 'WARN', msg: pgDeg  + ' PG' + (pgDeg  > 1 ? 's' : '') + ' degraded' });
    if (pgUs  > 0) checks.push({ sev: 'WARN', msg: pgUs   + ' PG' + (pgUs   > 1 ? 's' : '') + ' undersized' });
    if (pgRec > 0) checks.push({ sev: 'WARN', msg: pgRec  + ' PG' + (pgRec  > 1 ? 's' : '') + ' recovering' });
    if (pgBf  > 0) checks.push({ sev: 'WARN', msg: pgBf   + ' PG' + (pgBf   > 1 ? 's' : '') + ' backfilling' });
    if (pgBfW > 0) checks.push({ sev: 'WARN', msg: pgBfW  + ' PG' + (pgBfW  > 1 ? 's' : '') + ' backfill wait' });

    // MDS cap-revoke evictions: client failed to return caps in time — severe escalation
    const totalCapEvict = (metrics['ceph_mds_server_cap_revoke_eviction'] || [])
        .reduce((s, e) => s + e.value, 0);
    if (totalCapEvict > 0) checks.push({ sev: 'ERR', msg: totalCapEvict + ' MDS cap-revoke eviction' + (totalCapEvict > 1 ? 's' : '') });

    // MDS slow replies: rate > 0 is the leading signal for cap-pressure stalls.
    // Uses mdsCounterHistory populated by the previous updateMDSDaemons call.
    let totalSlowRate = 0;
    for (const [daemon, hist] of Object.entries(mdsCounterHistory)) {
        if (hist.length < 2) continue;
        const oldest = hist[0], newest = hist[hist.length - 1];
        const elapsed = (newest.ts - oldest.ts) / 1000;
        if (elapsed > 0) totalSlowRate += Math.max(0, (newest.slowReply - oldest.slowReply) / elapsed);
    }
    if (totalSlowRate > 0.05) checks.push({ sev: 'WARN', msg: 'MDS slow replies: ' + totalSlowRate.toFixed(1) + '/s' });

    return checks;
}

function updateHealthDetail(metrics) {
    const el = document.getElementById('healthDetail');
    const status = scalar(metrics, 'ceph_health_status', -1);
    if (status <= 0) { el.innerHTML = ''; return; }

    const checks = inferHealthChecks(metrics);
    if (checks.length === 0) {
        el.style.color = '#9ca3af';
        el.innerHTML = 'cause not visible in Prometheus metrics<br>' +
            'connect restful or dashboard mgr module for details<br>' +
            '<span style="font-style:italic">see HEALTH_API_PLAN.md</span>';
        return;
    }
    el.style.color = '';
    el.innerHTML = checks.map(c =>
        '<span style="color:' + (c.sev === 'ERR' ? '#ef4444' : '#eab308') + '">' + c.msg + '</span>'
    ).join('<br>');
}

function updateCluster(metrics) {
    const total   = scalar(metrics, 'ceph_cluster_total_bytes');
    const used    = scalar(metrics, 'ceph_cluster_total_used_bytes');
    const raw     = scalar(metrics, 'ceph_cluster_total_used_raw_bytes');
    const objs    = sumVec(metrics, 'ceph_pool_objects');
    const stored  = sumVec(metrics, 'ceph_pool_stored');

    const pct = total > 0 ? (used / total * 100).toFixed(1) : 0;
    document.getElementById('clusterUsedPct').textContent   = pct + '%';
    document.getElementById('clusterUsedBytes').textContent = formatBytes(used);
    document.getElementById('clusterTotalBytes').textContent = formatBytes(total);
    document.getElementById('clusterRawUsed').textContent   = formatBytes(raw);
    document.getElementById('clusterObjects').textContent   = objs.toLocaleString();

    document.getElementById('clusterStoredBytes').textContent = stored > 0 ? formatBytes(stored) : '-';
    document.getElementById('clusterRawUsed2').textContent    = raw   > 0 ? formatBytes(raw)    : '-';
    const overhead = stored > 0 ? '\xD7' + (raw / stored).toFixed(2) : '-';
    document.getElementById('clusterStoredOverhead').textContent = overhead;

    const now = new Date();
    pushHistoryLabel(usageHistory, now);
    pushHistory(usageHistory.pct, parseFloat(pct));
    updateChartData(charts.usage, usageHistory.labels);
    updateChartLegend('usageChartLegend', [
        {label: 'Used %', color: usageHistory.pct.color, data: usageHistory.pct.data,
         fmt: v => v.toFixed(1) + '%'},
    ]);
    pushSparkline(charts.usageSparkline, parseFloat(pct));
}

function escHtml(s) {
    return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function formatPromGraphValue(v, unit, integer) {
    if (unit === 's') {
        if (v >= 1)     return v.toFixed(2) + 's';
        if (v >= 0.001) return (v * 1000).toFixed(1) + 'ms';
        return v.toPrecision(3) + 'ms';
    }
    if (unit === 'ms') {
        // value is already in milliseconds
        if (v >= 1000)  return (v / 1000).toFixed(2) + 's';
        if (v >= 1)     return v.toFixed(1) + 'ms';
        if (v >= 0.001) return (v * 1000).toFixed(0) + 'μs';
        return v.toPrecision(3) + 'μs';
    }
    if (integer) return Math.round(v).toLocaleString();
    if (v >= 1e6) return (v / 1e6).toFixed(2) + 'M';
    if (v >= 1e3) return (v / 1e3).toFixed(2) + 'k';
    if (v >= 1)   return v.toFixed(2);
    if (v >= 0.001) return (v * 1000).toFixed(1) + 'ms';
    return v.toPrecision(3);
}

function relativeColor(i, total) {
    // Returns a color from red (index 0, worst/highest) to green (last, best/lowest).
    if (total <= 1) return '#22c55e';
    const t = i / (total - 1); // 0=first/worst, 1=last/best
    if (t < 0.33) return '#ef4444'; // red
    if (t < 0.55) return '#f97316'; // orange
    if (t < 0.75) return '#eab308'; // yellow
    return '#22c55e';              // green
}

function initDynGraphs(graphs) {
    const container = document.getElementById('dynGraphContainer');
    if (!container) return;
    // Destroy existing dynamic charts and remove only sec-dg-* elements,
    // preserving static sections like sec-mds-trim that live in the same grid.
    Object.values(dynGraphState).forEach(s => { if (s.chart) { s.chart.destroy(); } });
    dynGraphState = {};
    container.querySelectorAll('[id^="sec-dg-"]').forEach(el => el.remove());
    for (const g of (graphs || [])) {
        const thresholds = (g.warn != null && g.crit != null) ? { warn: g.warn, crit: g.crit } : null;
        const noteHtml = g.note
            ? '<div class="text-xs text-yellow-600 dark:text-yellow-400 mb-2">' + escHtml(g.note) + '</div>'
            : '';
        const spanClass = g.full_width ? 'col-span-2' : '';
        const queryTitle = g.query ? ' title="PromQL: ' + escHtml(g.query) + '"' : '';
        const html =
            '<section id="sec-dg-' + g.id + '" class="bg-white dark:bg-gray-800 shadow rounded-lg p-4 hidden ' + spanClass + '">' +
            '<div class="mb-3 flex items-start gap-2">' +
            '<div class="flex-1"' + queryTitle + '>' +
            '<h2 class="text-base font-semibold text-gray-700 dark:text-gray-200">' + escHtml(g.title || g.id) + '</h2>' +
            (g.description ? '<p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">' + escHtml(g.description) + '</p>' : '') +
            '</div>' +
            '<button onclick="openPromGraphModal(' + escHtml(JSON.stringify(g.id)) + ')" title="Expand" class="flex-shrink-0" style="width:20px;height:20px;border:none;background:none;cursor:pointer;font-size:14px;padding:0;line-height:1;opacity:0.35;color:inherit" onmouseover="this.style.opacity=\'0.8\'" onmouseout="this.style.opacity=\'0.35\'">⛶</button>' +
            '</div>' +
            noteHtml +
            '<div id="dg-' + g.id + '-grid" style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:0.375rem;margin-bottom:0.5rem"></div>' +
            '<div class="relative" style="height:120px"><canvas id="dg-' + g.id + '-chart"></canvas></div>' +
            '<div id="dg-' + g.id + '-legend" class="mt-2"></div>' +
            '</section>';
        // Insert before sec-mds-trim so configured graphs appear first;
        // the last panel (cap-ceiling) appends after sec-mds-trim so trim sits second-to-last.
        const trimSection = container.querySelector('#sec-mds-trim');
        const isLast = g === graphs[graphs.length - 1];
        if (trimSection && !isLast) container.insertBefore(document.createRange().createContextualFragment(html), trimSection);
        else container.insertAdjacentHTML('beforeend', html);
        const chart = makeTimeLineChart('dg-' + g.id + '-chart', [], null, thresholds);
        dynGraphState[g.id] = { history: { labels: [], series: {} }, colorIdx: 0, chart, _cfg: g };
    }
}

function updateDynGraph(graphId, rows, cfg) {
    const section = document.getElementById('sec-dg-' + graphId);
    const st = dynGraphState[graphId];
    if (!section || !st) return;
    const alwaysShow = cfg && cfg.always_show;
    if (!Array.isArray(rows) || rows.length === 0) {
        if (!alwaysShow) {
            section.classList.add('hidden');
            return;
        }
        // always_show: keep section visible with an "all clear" state
        section.classList.remove('hidden');
        const grid = document.getElementById('dg-' + graphId + '-grid');
        if (grid) grid.innerHTML = '<p class="text-xs text-gray-400 dark:text-gray-500 col-span-3 py-1">— all clear, no active entries —</p>';
        if (st.chart) { st.chart.data.datasets = []; updateChartData(st.chart, st.history.labels); }
        return;
    }
    section.classList.remove('hidden');
    const grid = document.getElementById('dg-' + graphId + '-grid');
    if (!grid) return;
    const thresholds = (cfg && cfg.warn != null && cfg.crit != null) ? { warn: cfg.warn, crit: cfg.crit } : null;
    const unit = cfg && cfg.unit;
    const integer = cfg && cfg.integer;
    const colorRelative = cfg && cfg.color_relative;
    const visibleRows = (cfg && cfg.production_filter && !showAllMDS)
        ? rows.filter(r => matchesProductionFS(r.name))
        : rows;
    grid.innerHTML = visibleRows.map((row, i) => {
        const val = row.value;
        let color;
        if (thresholds)           color = thresholdColor(val, thresholds);
        else if (colorRelative)   color = relativeColor(i, visibleRows.length);
        else                      color = '#3b82f6';
        // Hex color → rgba with low opacity for card background tint
        const hex = color.replace('#','');
        const r = parseInt(hex.slice(0,2),16), g2 = parseInt(hex.slice(2,4),16), b = parseInt(hex.slice(4,6),16);
        const bgStyle = 'background-color:rgba(' + r + ',' + g2 + ',' + b + ',0.12)';
        return '<div class="p-2 rounded-lg" style="' + bgStyle + '">' +
            '<p class="text-xs font-medium text-gray-500 dark:text-gray-400 truncate" title="' + escHtml(row.name) + '">' + escHtml(row.name) + '</p>' +
            '<p class="text-sm font-bold mt-0.5" style="color:' + color + '">' + formatPromGraphValue(val, unit, integer) + '</p>' +
            '</div>';
    }).join('');
    // History tracks all rows (not just visible) so filter-mode toggles don't lose data.
    pushHistoryLabel(st.history, new Date());
    const seen = new Set();
    for (const row of rows) {
        seen.add(row.name);
        let s = st.history.series[row.name];
        if (!s) {
            const color = PROM_QUERY_PALETTE[st.colorIdx % PROM_QUERY_PALETTE.length];
            st.colorIdx++;
            s = st.history.series[row.name] = { data: [], color, bg: color.replace(',1)', ',0.15)') };
            while (s.data.length < st.history.labels.length - 1) s.data.push(null);
        }
        pushHistory(s, row.value);
    }
    for (const [name, s] of Object.entries(st.history.series)) {
        if (seen.has(name)) continue;
        pushHistory(s, null);
        const recent = s.data.slice(-SERIES_PRUNE_AFTER);
        if (recent.length >= SERIES_PRUNE_AFTER && recent.every(v => v === null))
            delete st.history.series[name];
    }
    const chartEntries = (cfg && cfg.production_filter && !showAllMDS)
        ? Object.entries(st.history.series).filter(([name]) => matchesProductionFS(name))
        : Object.entries(st.history.series);
    if (st.chart) {
        st.chart.data.datasets = chartEntries.map(([name, s]) => mkDs(name, s, false));
        updateChartData(st.chart, st.history.labels);
    }
    updateChartLegend('dg-' + graphId + '-legend', chartEntries.map(([name, s]) => ({
        label: name, color: s.color,
        data: s.data.filter(v => v !== null && v !== undefined),
        fmt: v => formatPromGraphValue(v, unit, integer),
    })));
    if (_promModalOpen === graphId) _refreshPromGraphModal();
}

// ─── Prom-graph fullscreen modal ──────────────────────────────────────────────
let _promModalOpen = null;
let _promModalChart = null;

function openPromGraphModal(graphId) {
    _promModalOpen = graphId;
    const st   = dynGraphState[graphId];
    const dark = isDark();
    const modal  = document.getElementById('prom-graph-modal');
    const panel  = modal.firstElementChild;
    const header = document.getElementById('prom-graph-modal-header');
    const title  = document.getElementById('prom-graph-modal-title');
    const desc   = document.getElementById('prom-graph-modal-desc');
    const clust  = document.getElementById('prom-graph-modal-cluster');
    const body   = document.getElementById('prom-graph-modal-body');
    panel.style.background   = dark ? 'rgb(31,41,55)' : '#fff';
    header.style.borderColor = dark ? 'rgba(255,255,255,0.1)' : '#e5e7eb';
    panel.querySelector('button').style.background = dark ? 'rgba(255,255,255,0.12)' : 'rgba(0,0,0,0.08)';
    panel.querySelector('button').style.color = dark ? '#e5e7eb' : '#374151';
    const cfg = (st && st._cfg) || {};
    title.textContent = cfg.title || graphId;
    title.style.color = dark ? '#f3f4f6' : '#111827';
    desc.textContent  = cfg.description || '';
    clust.textContent = (typeof currentCluster !== 'undefined' ? currentCluster : '');
    if (_promModalChart) { try { _promModalChart.destroy(); } catch(e) {} _promModalChart = null; }
    _renderPromGraphModalBody(body, graphId);
    modal.hidden = false;
    document.addEventListener('keydown', _promModalKeyHandler);
}

function _renderPromGraphModalBody(body, graphId) {
    const st  = dynGraphState[graphId];
    const cfg = (st && st._cfg) || {};
    const unit = cfg.unit, integer = cfg.integer;
    const thresholds = (cfg.warn != null && cfg.crit != null) ? { warn: cfg.warn, crit: cfg.crit } : null;
    const dark = isDark();
    const textColor   = dark ? '#d1d5db' : '#374151';
    const borderColor = dark ? 'rgba(255,255,255,0.08)' : '#e5e7eb';
    const entries = st ? Object.entries(st.history.series) : [];
    const visEntries = (cfg.production_filter && !showAllMDS)
        ? entries.filter(([name]) => matchesProductionFS(name))
        : entries;
    const currentRows = visEntries
        .map(([name, s]) => ({ name, value: s.data[s.data.length - 1], color: s.color }))
        .filter(r => r.value !== null && r.value !== undefined && !isNaN(r.value))
        .sort((a, b) => b.value - a.value);
    const tblRows = currentRows.length === 0
        ? '<tr><td colspan="2" style="padding:10px;text-align:center;opacity:0.4;font-size:13px">— no data —</td></tr>'
        : currentRows.map(r => {
            const color = thresholds ? thresholdColor(r.value, thresholds) : r.color;
            return '<tr style="border-bottom:1px solid ' + borderColor + '">' +
                '<td style="padding:5px 10px;font-size:13px;color:' + textColor + '">' + escHtml(r.name) + '</td>' +
                '<td style="padding:5px 10px;font-size:13px;font-weight:700;text-align:right;color:' + color + '">' +
                formatPromGraphValue(r.value, unit, integer) + '</td>' +
                '</tr>';
        }).join('');
    body.innerHTML =
        '<div style="flex:1;min-height:0;height:55vh"><canvas id="prom-graph-modal-chart"></canvas></div>' +
        '<div style="overflow-y:auto;max-height:30vh">' +
        '<table style="width:100%;border-collapse:collapse">' +
        '<thead><tr style="border-bottom:2px solid ' + borderColor + '">' +
        '<th style="padding:4px 10px;font-size:11px;text-align:left;opacity:0.5;font-weight:600">Series</th>' +
        '<th style="padding:4px 10px;font-size:11px;text-align:right;opacity:0.5;font-weight:600">Current</th>' +
        '</tr></thead><tbody>' + tblRows + '</tbody></table></div>';
    const datasets = visEntries.map(([name, s]) => mkDs(name, s, false));
    _promModalChart = makeTimeLineChart('prom-graph-modal-chart', datasets, null, thresholds);
    if (_promModalChart && st) {
        _promModalChart.data.labels = st.history.labels;
        _promModalChart.update('none');
    }
}

function closePromGraphModal() {
    _promModalOpen = null;
    if (_promModalChart) { try { _promModalChart.destroy(); } catch(e) {} _promModalChart = null; }
    const modal = document.getElementById('prom-graph-modal');
    if (modal) modal.hidden = true;
    document.removeEventListener('keydown', _promModalKeyHandler);
}

function _promModalKeyHandler(e) { if (e.key === 'Escape') closePromGraphModal(); }

function _refreshPromGraphModal() {
    if (!_promModalOpen) return;
    const body = document.getElementById('prom-graph-modal-body');
    if (!body) return;
    if (_promModalChart) { try { _promModalChart.destroy(); } catch(e) {} _promModalChart = null; }
    _renderPromGraphModalBody(body, _promModalOpen);
}

`
