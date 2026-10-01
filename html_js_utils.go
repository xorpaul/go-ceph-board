package main

const htmlJSUtils = `
<script>
// ─── Debug ───────────────────────────────────────────────────────────────────
const DBG = false; // set true to enable debug logging
const dbg = (...args) => DBG && console.log('[ceph-board]', ...args);
dbg('script loaded');

function debugPageHeight(label) {
    if (!DBG) return;
    const h = document.documentElement.scrollHeight;
    const w = document.documentElement.scrollWidth;
    dbg(label, 'scrollHeight=' + h + ' scrollWidth=' + w + ' innerHeight=' + window.innerHeight);
}

function debugChartSizes(label) {
    if (!DBG) return;
    const ids = ['pgChart','osdChart','usageChart','osdSparkline','usageSparkline'];
    ids.forEach(function(id) {
        const el = document.getElementById(id);
        const ph = el && el.parentElement ? el.parentElement.offsetHeight : '?';
        if (el) dbg(label, id, 'canvas=' + el.width + 'x' + el.height + ' css=' + el.style.width + 'x' + el.style.height + ' parentH=' + ph);
    });
}

// ─── Theme ───────────────────────────────────────────────────────────────────
const themeToggle = document.getElementById('themeToggle');
const themeIcon   = document.getElementById('themeIcon');
const charts = {};
let dynGraphState = {}; // declared here so initTheme() → updateAllChartColors() can access it

function applyTheme(theme) {
    if (theme === 'dark') {
        document.documentElement.classList.add('dark');
        document.body.classList.add('dark');
        themeIcon.textContent = '☀️';
    } else {
        document.documentElement.classList.remove('dark');
        document.body.classList.remove('dark');
        themeIcon.textContent = '🌙';
    }
    localStorage.setItem('theme', theme);
    updateAllChartColors();
}
function initTheme() {
    applyTheme(localStorage.getItem('theme') || 'dark');
}
function toggleTheme() {
    applyTheme(document.documentElement.classList.contains('dark') ? 'light' : 'dark');
}
themeToggle.addEventListener('click', toggleTheme);
initTheme();

// ─── Prometheus text parser ───────────────────────────────────────────────────
function parsePrometheusText(text) {
    const result = {};
    const lines = text.split('\n');
    for (const line of lines) {
        const trimmed = line.trim();
        if (trimmed === '' || trimmed.startsWith('#')) continue;

        // Split metric descriptor from value, handling label values that contain spaces
        // (e.g. ceph_version="ceph version 18.2.7 ... reef (stable)").
        // For labelled metrics "name{...} value [ts]", split at the closing "}" so
        // spaces inside quoted label values don't confuse the split.
        let metricPart, valueStr;
        const braceClose = trimmed.indexOf('}');
        if (braceClose !== -1) {
            metricPart = trimmed.substring(0, braceClose + 1);
            valueStr   = trimmed.substring(braceClose + 1).trimStart().split(' ')[0];
        } else {
            const sp = trimmed.indexOf(' ');
            if (sp === -1) continue;
            metricPart = trimmed.substring(0, sp);
            valueStr   = trimmed.substring(sp + 1).split(' ')[0];
        }
        const value = parseFloat(valueStr);
        if (isNaN(value)) continue;

        const braceIdx = metricPart.indexOf('{');
        let metricName, labels;
        if (braceIdx === -1) {
            metricName = metricPart;
            labels = {};
        } else {
            metricName = metricPart.substring(0, braceIdx);
            labels = {};
            const labelsStr = metricPart.substring(braceIdx + 1, metricPart.length - 1);
            for (const m of labelsStr.matchAll(/(\w+)="([^"]*)"/g)) {
                labels[m[1]] = m[2];
            }
        }

        if (!result[metricName]) result[metricName] = [];
        result[metricName].push({ value, labels });
    }
    return result;
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
function scalar(metrics, name, def = 0) {
    const arr = metrics[name];
    return (arr && arr.length > 0) ? arr[0].value : def;
}

// Sum all sample values for a metric (handles per-pool label variants like ceph_pg_total{pool_id=...}).
function sumVec(metrics, name, def = 0) {
    const arr = metrics[name];
    if (!arr || arr.length === 0) return def;
    return arr.reduce((s, e) => s + e.value, 0);
}

function formatBytes(bytes) {
    if (bytes === undefined || bytes === null || isNaN(bytes)) return '-';
    const units = ['B','KiB','MiB','GiB','TiB','PiB'];
    let v = bytes, i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return v.toFixed(i === 0 ? 0 : 2) + ' ' + units[i];
}

function fmtCount(n) {
    if (n >= 1e9) return (n / 1e9).toFixed(1) + 'B';
    if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
    if (n >= 1e3) return (n / 1e3).toFixed(0) + 'k';
    return n.toString();
}

const UNIT_STYLES = {
    'TiB': 'background:rgba(239,68,68,0.18);border-radius:3px;padding:0 3px;',
    'GiB': 'background:rgba(249,115,22,0.18);border-radius:3px;padding:0 3px;',
};

function formatBytesHtml(bytes) {
    if (bytes === undefined || bytes === null || isNaN(bytes)) return '-';
    const units = ['B','KiB','MiB','GiB','TiB','PiB'];
    let v = bytes, i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    const unit = units[i];
    const num  = v.toFixed(i === 0 ? 0 : 2);
    const style = UNIT_STYLES[unit];
    if (style) return num + ' <span style="' + style + '">' + unit + '</span>';
    return num + ' ' + unit;
}

function diskColor(pct) {
    if (pct >= 90) return '#ef4444';
    if (pct >= 80) return '#f97316';
    if (pct >= 70) return '#eab308';
    return '#22c55e';
}

function latencyColor(ms) {
    if (ms >= 50) return '#ef4444';
    if (ms >= 20) return '#f97316';
    if (ms >= 10) return '#eab308';
    return '#22c55e';
}

// thresholdColor returns a status color for value against a {warn, crit} pair.
// Falls back to green when thresholds is null/undefined.
// ─── Production CephFS volume filter helpers ──────────────────────────────────
// These reference clusterProductionCephFSVolumes / currentCluster / showAllMDS which are
// declared in html_js_main.go and live in the same script scope.

// productionCephFSVolumeSet returns a Set of production FS names for the current cluster.
// An empty set means "no list configured → treat all as production".
function productionMDSSet() {
    return new Set((typeof clusterProductionCephFSVolumes !== 'undefined' ? clusterProductionCephFSVolumes[currentCluster] : null) || []);
}

// isProductionFS returns true if fsName (e.g. "fs1") is in the production list,
// or if no production list is configured for this cluster.
function isProductionFS(fsName) {
    const prod = productionMDSSet();
    return prod.size === 0 || prod.has(fsName);
}

// matchesProductionFS checks whether a series name (e.g. "hosting_a01") contains
// any production FS name as a substring, used for the generic Prometheus Query panel.
function matchesProductionFS(seriesName) {
    const prod = productionMDSSet();
    if (prod.size === 0) return true;
    const lower = seriesName.toLowerCase();
    for (const p of prod) {
        if (lower.includes(p.toLowerCase())) return true;
    }
    return false;
}

function thresholdColor(value, thresholds) {
    if (!thresholds) return '#22c55e';
    if (value >= thresholds.crit) return '#ef4444';
    if (value >= thresholds.warn) return '#eab308';
    return '#22c55e';
}

// makeThresholdBgPlugin returns a Chart.js per-chart plugin that draws colored
// background bands for warn and crit zones. Call isDark() inside beforeDraw so
// the alpha responds to live theme toggles without needing a chart rebuild.
function makeThresholdBgPlugin(thresholds) {
    if (!thresholds) return null;
    return {
        id: 'thresholdBg',
        beforeDraw(chart) {
            const { ctx, chartArea, scales } = chart;
            if (!chartArea || !scales.y) return;
            const alpha = isDark() ? 0.09 : 0.07;
            const { top, bottom, left, right } = chartArea;
            const py = v => Math.max(top, Math.min(bottom, scales.y.getPixelForValue(v)));
            const critPx = py(thresholds.crit);
            const warnPx = py(thresholds.warn);
            ctx.save();
            ctx.beginPath();
            ctx.rect(left, top, right - left, bottom - top);
            ctx.clip();
            ctx.fillStyle = 'rgba(239,68,68,' + alpha + ')';
            ctx.fillRect(left, top, right - left, critPx - top);
            ctx.fillStyle = 'rgba(234,179,8,' + alpha + ')';
            ctx.fillRect(left, critPx, right - left, warnPx - critPx);
            ctx.fillStyle = 'rgba(34,197,94,' + alpha + ')';
            ctx.fillRect(left, warnPx, right - left, bottom - warnPx);
            ctx.restore();
        }
    };
}

// configSites holds the "sites" list from /clusters: [{match, label, color, bg}].
// The first entry whose match is a substring of the hostname (or its CRUSH
// labels) names the site; without any configured sites every host is 'unknown'.
let configSites = [];

function getSiteFromHostname(hostname, crushLabels) {
    if (!hostname) return 'unknown';
    const h = (hostname + ' ' + (crushLabels || '')).toLowerCase();
    for (const s of configSites) {
        if (s.match && h.includes(s.match.toLowerCase())) return s.label || s.match.toUpperCase();
    }
    return 'unknown';
}

// hostLinkHTML returns the optional per-host link (host_link in the config),
// e.g. to a CMDB search, shown as a 🖥️ icon, or '' when none is configured.
let configHostLink = null;
function hostLinkHTML(hostname) {
    if (!configHostLink || !configHostLink.url || !hostname) return '';
    const url = configHostLink.url.split('{host}').join(encodeURIComponent(hostname));
    const title = (configHostLink.title || 'Open') + ' ' + hostname;
    const esc = s => String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;');
    return ' <a href="' + esc(url) + '" target="_blank" rel="noopener" title="' + esc(title) + '" ' +
        'style="text-decoration:none;font-size:0.8em;vertical-align:middle;opacity:0.7">&#128421;&#65039;</a>';
}

// extractIPFromAddr pulls the first IPv4 address out of a Ceph public_addr label.
// Handles both "1.2.3.4:port/nonce" and "[v2:1.2.3.4:port/nonce,v1:...]" formats.
function extractIPFromAddr(addr) {
    if (!addr) return '';
    const m = addr.match(/(\d+\.\d+\.\d+\.\d+)/);
    return m ? m[1] : '';
}

// buildHostSiteMap resolves hostname→site for all entries in a ceph_mds_metadata array,
// including daemons whose hostname lacks a site marker (e.g. "mds1" instead of "mds1.site2").
// Resolution works by cross-referencing the daemon's IP (from public_addr) with peers
// on the same host that do have a recognisable site in their hostname.
function buildHostSiteMap(metaArr) {
    const ipSite = {};
    for (const m of metaArr) {
        const site = getSiteFromHostname(m.labels.hostname || '', '');
        if (site === 'unknown') continue;
        const ip = extractIPFromAddr(m.labels.public_addr || '');
        if (ip) ipSite[ip] = site;
    }
    const map = {};
    for (const m of metaArr) {
        const hn = m.labels.hostname || m.labels.ceph_daemon || '';
        if (!hn || map[hn] !== undefined) continue;
        let site = getSiteFromHostname(hn, '');
        if (site === 'unknown') {
            const ip = extractIPFromAddr(m.labels.public_addr || '');
            if (ip && ipSite[ip]) site = ipSite[ip];
        }
        map[hn] = site;
    }
    return map;
}

// buildHostCanonMap detects when multiple hostnames share the same public_addr IP
// (e.g. "mds1" and "mds1.site2" on the same machine) and returns a map from each
// non-canonical variant to the canonical (most-qualified) name so that grouping
// functions can merge them into a single host entry.
function buildHostCanonMap(metaArr) {
    const ipHosts = {};
    for (const m of metaArr) {
        const ip = extractIPFromAddr(m.labels.public_addr || '');
        const hn = m.labels.hostname || m.labels.ceph_daemon || '';
        if (!ip || !hn) continue;
        if (!ipHosts[ip]) ipHosts[ip] = new Set();
        ipHosts[ip].add(hn);
    }
    const map = {};
    for (const hns of Object.values(ipHosts)) {
        if (hns.size <= 1) continue;
        const arr = [...hns];
        // most dots wins (longest FQDN); on tie, longer string wins
        const canonical = arr.reduce((best, h) => {
            const bd = best.split('.').length, hd = h.split('.').length;
            return hd > bd || (hd === bd && h.length > best.length) ? h : best;
        });
        for (const h of arr) {
            if (h !== canonical) map[h] = canonical;
        }
    }
    return map;
}

// Default site colours, by position in the sites config.
const SITE_PALETTE = [
    { color: '#3b82f6', bg: '#1e40af' },  // blue
    { color: '#10b981', bg: '#047857' },  // green
    { color: '#f59e0b', bg: '#b45309' },  // amber
    { color: '#a78bfa', bg: '#6d28d9' },  // violet
    { color: '#f472b6', bg: '#be185d' },  // pink
    { color: '#22d3ee', bg: '#0e7490' },  // cyan
];
const SITE_UNKNOWN = { color: '#6b7280', bg: '#374151' };

function siteColor(site) {
    for (let i = 0; i < configSites.length; i++) {
        const s = configSites[i];
        if ((s.label || (s.match || '').toUpperCase()) !== site) continue;
        const p = SITE_PALETTE[i % SITE_PALETTE.length];
        return { color: s.color || p.color, bg: s.bg || p.bg };
    }
    return SITE_UNKNOWN;
}

const FS_PALETTE = [
    '#2563eb', // blue
    '#0d9488', // teal
    '#7c3aed', // purple
    '#ea580c', // orange
    '#0891b2', // cyan
    '#65a30d', // lime
    '#db2777', // pink
    '#4f46e5', // indigo
    '#d97706', // amber
    '#059669', // emerald
    '#e11d48', // rose
    '#a21caf', // fuchsia
    '#0284c7', // sky
    '#ca8a04', // yellow
];
// fsId is the numeric Ceph fs_id; returns a stable hex color string.
function fsColor(fsId) {
    const idx = (parseInt(fsId, 10) - 1 + FS_PALETTE.length) % FS_PALETTE.length;
    return FS_PALETTE[idx < 0 ? 0 : idx];
}

// ─── Ceph version helpers ─────────────────────────────────────────────────────
// parseCephVersion parses a full ceph_version label value like
// "ceph version 18.2.7 (hash) reef (stable)" into {short, codename, full}.
function parseCephVersion(fullStr) {
    if (!fullStr) return null;
    const m = fullStr.match(/ceph version (\S+) \([^)]+\) (\w+)/);
    if (!m) return { short: fullStr, codename: '', full: fullStr };
    return { short: m[1], codename: m[2], full: fullStr };
}

// majorityVersion returns the most common ceph_version label value found in a
// metadata metric array (e.g. ceph_osd_metadata entries). Returns null when
// the array is empty or no ceph_version labels are present.
function majorityVersion(metaArr) {
    const counts = {};
    for (const m of metaArr) {
        const v = m.labels.ceph_version;
        if (v) counts[v] = (counts[v] || 0) + 1;
    }
    const keys = Object.keys(counts);
    if (keys.length === 0) return null;
    return keys.sort((a, b) => counts[b] - counts[a])[0];
}

// cephVersionCellHtml renders a compact version cell. When majorityFull is
// provided and the version differs from it (rolling upgrade in progress), the
// cell is highlighted amber so the outlier is immediately visible.
function cephVersionCellHtml(versionFull, majorityFull) {
    if (!versionFull) return '<span class="text-xs text-gray-400">—</span>';
    const parsed = parseCephVersion(versionFull);
    const display = parsed ? parsed.short : versionFull;
    const isMixed = majorityFull && versionFull !== majorityFull;
    if (isMixed) {
        return '<span class="text-xs font-mono font-semibold" style="color:#d97706;background:rgba(245,158,11,0.15);border-radius:4px;padding:1px 5px" title="' + versionFull + '">' + display + '</span>';
    }
    return '<span class="text-xs font-mono" style="color:#6b7280" title="' + versionFull + '">' + display + '</span>';
}

// ─── Chart setup ─────────────────────────────────────────────────────────────
const MAX_HISTORY = 30;

function isDark() { return document.documentElement.classList.contains('dark'); }
function chartTextColor()  { return isDark() ? '#F3F4F6' : '#374151'; }
function chartGridColor()  { return isDark() ? '#4B5563' : '#E5E7EB'; }

function updateAllChartColors() {
    const tc = chartTextColor(), gc = chartGridColor();
    function applyColors(chart) {
        if (!chart || !chart.options) return;
        if (chart.options.scales) {
            Object.values(chart.options.scales).forEach(s => {
                if (s.ticks) s.ticks.color = tc;
                if (s.grid)  s.grid.color  = gc;
            });
        }
        if (chart.options.plugins?.legend?.labels)
            chart.options.plugins.legend.labels.color = tc;
        chart.update('none');
    }
    Object.values(charts).forEach(applyColors);
    Object.values(dynGraphState).forEach(s => applyColors(s.chart));
}

function makeTimeLineChart(canvasId, datasets, yMax, thresholds) {
    const canvas = document.getElementById(canvasId);
    if (!canvas) return null;
    const tc = chartTextColor(), gc = chartGridColor();
    const opt = {
        animation: false,
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
            legend: { display: false },
            tooltip: { mode: 'index', intersect: false,
                callbacks: { label: ctx => ' ' + ctx.dataset.label + ': ' + ctx.parsed.y } }
        },
        scales: {
            x: { type: 'time', time: { unit: 'second', displayFormats: { second: 'HH:mm:ss' } },
                 ticks: { color: tc, maxTicksLimit: 5, font: { size: 9 } },
                 grid:  { color: gc } },
            y: { ticks: { color: tc, font: { size: 9 } }, grid: { color: gc },
                 beginAtZero: true, ...(yMax ? { max: yMax } : {}) }
        }
    };
    const plugins = [];
    const bgPlugin = makeThresholdBgPlugin(thresholds || null);
    if (bgPlugin) plugins.push(bgPlugin);
    return new Chart(canvas, { type: 'line', data: { datasets }, options: opt, plugins });
}

function makeSparkline(canvasId, color) {
    const canvas = document.getElementById(canvasId);
    if (!canvas) return null;
    const ds = [{ data: [], borderColor: color, backgroundColor: color.replace('1)', '0.15)'),
                  fill: true, tension: 0.4, pointRadius: 0, borderWidth: 1.5 }];
    return new Chart(canvas, {
        type: 'line',
        data: { labels: [], datasets: ds },
        options: {
            animation: false, responsive: true, maintainAspectRatio: false,
            plugins: { legend: { display: false } },
            scales: { x: { display: false }, y: { display: false, beginAtZero: true } }
        }
    });
}

function pushSparkline(chart, value) {
    if (!chart) return;
    const now = new Date();
    if (chart.data.labels.length === 0) {
        chart.data.labels.push(new Date(now.getTime() - 1000));
        chart.data.datasets[0].data.push(value);
    }
    chart.data.labels.push(now);
    chart.data.datasets[0].data.push(value);
    while (chart.data.labels.length > MAX_HISTORY) {
        chart.data.labels.shift();
        chart.data.datasets[0].data.shift();
    }
    chart.update('none');
}

// History buffers for time-series charts
const pgHistory = {
    labels: [],
    clean:       { data: [], color: 'rgba(34,197,94,1)',    bg: 'rgba(34,197,94,0.15)' },
    degraded:    { data: [], color: 'rgba(239,68,68,1)',    bg: 'rgba(239,68,68,0.15)' },
    recovering:  { data: [], color: 'rgba(249,115,22,1)',   bg: 'rgba(249,115,22,0.15)' },
    backfilling: { data: [], color: 'rgba(245,158,11,1)',   bg: 'rgba(245,158,11,0.15)' },
    backfillWait:{ data: [], color: 'rgba(252,211,77,1)',   bg: 'rgba(252,211,77,0.15)' },
    undersized:  { data: [], color: 'rgba(139,92,246,1)',   bg: 'rgba(139,92,246,0.15)' },
};
// Backfill ETA: sliding window of (timestamp, pending-PG-count) samples.
// Capped at BACKFILL_WINDOW_MS; cleared when pending drops to 0 or cluster changes.
const BACKFILL_WINDOW_MS = 120_000;
const backfillEtaHistory = []; // [{ts: number, pending: number}, ...]

function fmtDuration(secs) {
    if (!isFinite(secs) || secs <= 0) return '?';
    secs = Math.round(secs);
    if (secs < 60)   return secs + 's';
    if (secs < 3600) { const m = Math.floor(secs/60); return m + 'm ' + (secs - m*60) + 's'; }
    const h = Math.floor(secs / 3600);
    const m = Math.round((secs % 3600) / 60);
    return h + 'h ' + (m > 0 ? m + 'm' : '');
}

const osdHistory = {
    labels: [],
    up:    { data: [], color: 'rgba(34,197,94,1)',  bg: 'rgba(34,197,94,0.1)' },
    in:    { data: [], color: 'rgba(59,130,246,1)', bg: 'rgba(59,130,246,0.1)' },
    total: { data: [], color: 'rgba(156,163,175,1)',bg: 'rgba(156,163,175,0.05)' },
};
const usageHistory = {
    labels: [],
    pct:   { data: [], color: 'rgba(16,185,129,1)', bg: 'rgba(16,185,129,0.15)' },
};
// Per-filesystem trim rate chart history: labels shared, series keyed by fs_id.
// byDaemon is keyed by ceph_daemon label (e.g. "mds.a") for per-daemon modal charts.
const mdsTrimChartHistory = { labels: [], byFs: {}, byDaemon: {} };

// Color palette for dynamic Prometheus graph series.
const PROM_QUERY_PALETTE = [
    'rgba(59,130,246,1)', 'rgba(239,68,68,1)', 'rgba(34,197,94,1)', 'rgba(249,115,22,1)',
    'rgba(139,92,246,1)', 'rgba(236,72,153,1)', 'rgba(20,184,166,1)', 'rgba(234,179,8,1)',
    'rgba(99,102,241,1)', 'rgba(107,114,128,1)',
];

// Prune a series from history after this many consecutive null ticks.
const SERIES_PRUNE_AFTER = 5;

// dynGraphState holds per-graph rendering state keyed by graph id.
// Each entry: { history: { labels:[], series:{} }, colorIdx: 0, chart: null }

function mkDs(label, hist, fill=true) {
    return { label, data: hist.data, borderColor: hist.color,
             backgroundColor: hist.bg, fill, tension: 0.4, pointRadius: 2, pointHoverRadius: 5,
             pointBackgroundColor: hist.color, borderWidth: 1.5 };
}

function initCharts() {
    const gt = typeof globalThresholds !== 'undefined' ? globalThresholds : null;
    charts.pg = makeTimeLineChart('pgChart', [
        mkDs('Clean',        pgHistory.clean),
        mkDs('Degraded',     pgHistory.degraded),
        mkDs('Recovering',   pgHistory.recovering, false),
        mkDs('Backfilling',  pgHistory.backfilling, false),
        mkDs('Backfill Wait',pgHistory.backfillWait, false),
        mkDs('Undersized',   pgHistory.undersized, false),
    ]);
    charts.osd = makeTimeLineChart('osdChart', [
        mkDs('Up', osdHistory.up),
        mkDs('In', osdHistory.in),
        mkDs('Total', osdHistory.total, false),
    ]);
    charts.usage = makeTimeLineChart('usageChart', [
        mkDs('Used %', usageHistory.pct),
    ], 100, gt ? gt.usage_pct : null);
    charts.osdSparkline   = makeSparkline('osdSparkline',   'rgba(34,197,94,1)');
    charts.usageSparkline = makeSparkline('usageSparkline', 'rgba(16,185,129,1)');
    charts.mdsTrim = makeTimeLineChart('mdsTrimChart', []);
}

function resetChartsAndHistory() {
    // Destroy all Chart.js instances so canvases can be reused
    Object.keys(charts).forEach(k => { if (charts[k]) { charts[k].destroy(); charts[k] = null; } });

    // Clear time-series history buffers
    pgHistory.labels.length = 0;
    pgHistory.clean.data.length = 0;
    pgHistory.degraded.data.length = 0;
    pgHistory.recovering.data.length = 0;
    pgHistory.backfilling.data.length = 0;
    pgHistory.backfillWait.data.length = 0;
    pgHistory.undersized.data.length = 0;
    osdHistory.labels.length = 0;
    osdHistory.up.data.length = 0;
    osdHistory.in.data.length = 0;
    osdHistory.total.data.length = 0;
    usageHistory.labels.length = 0;
    usageHistory.pct.data.length = 0;
    // Destroy dynamic graph charts and reset state
    Object.values(dynGraphState).forEach(s => { if (s.chart) { s.chart.destroy(); s.chart = null; } });
    dynGraphState = {};

    // Clear rate-tracking histories so first tick after switch starts fresh
    backfillEtaHistory.length = 0;
    Object.keys(poolCounterHistory).forEach(k => delete poolCounterHistory[k]);
    Object.keys(mdsCounterHistory).forEach(k => delete mdsCounterHistory[k]);
    Object.keys(mdsTrimHistory).forEach(k => delete mdsTrimHistory[k]);
    Object.keys(hostCpuHistory).forEach(k => delete hostCpuHistory[k]);
    // Clear trim chart history
    mdsTrimChartHistory.labels.length = 0;
    Object.keys(mdsTrimChartHistory.byFs).forEach(k => delete mdsTrimChartHistory.byFs[k]);

    // Clear all table bodies so stale rows from the previous cluster are gone
    ['mdsHostsTopTableBody', 'osdHostTableBody', 'monTableBody', 'mdsTableBody',
     'poolTableBody', 'mdsDaemonTableBody'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.innerHTML = '';
    });

    // Clear the MDS session Sankey (canvas is rendered dynamically into this container)
    const sankeyContainer = document.getElementById('mdsSankeyContainer');
    if (sankeyContainer) sankeyContainer.innerHTML = '';
}

function pushHistoryLabel(hist, now) {
    if (hist.labels.length === 0) hist.labels.push(new Date(now.getTime() - 1000));
    hist.labels.push(now);
    while (hist.labels.length > MAX_HISTORY) hist.labels.shift();
}

function pushHistory(hist, val) {
    if (hist.data.length === 0) hist.data.push(val);
    hist.data.push(val);
    while (hist.data.length > MAX_HISTORY) hist.data.shift();
}

function updateChartData(chart, histLabels) {
    if (!chart) return;
    chart.data.labels = histLabels;
    chart.update('none');
}

function updateChartLegend(containerId, series) {
    const el = document.getElementById(containerId);
    if (!el) return;
    const tc = isDark() ? 'text-gray-300' : 'text-gray-600';
    const hd = isDark() ? 'text-gray-400' : 'text-gray-400';
    const rows = series.map(s => {
        const d = s.data;
        if (d.length === 0) return '';
        const cur = d[d.length - 1];
        const min = Math.min(...d);
        const max = Math.max(...d);
        const avg = d.reduce((a, b) => a + b, 0) / d.length;
        const f = s.fmt || (v => Math.round(v).toLocaleString());
        return '<tr class="' + tc + '">' +
            '<td class="pr-1" style="color:' + s.color + '">&#9679;</td>' +
            '<td class="pr-4 font-medium whitespace-nowrap">' + s.label + '</td>' +
            '<td class="text-right tabular-nums px-3">' + f(cur) + '</td>' +
            '<td class="text-right tabular-nums px-3">' + f(min) + '</td>' +
            '<td class="text-right tabular-nums px-3">' + f(avg) + '</td>' +
            '<td class="text-right tabular-nums pl-3">' + f(max) + '</td>' +
            '</tr>';
    }).join('');
    el.innerHTML = '<table class="w-full text-xs border-collapse">' +
        '<thead><tr class="' + hd + ' text-xs">' +
        '<th colspan="2" class="text-left pb-1 font-medium">Series</th>' +
        '<th class="text-right pb-1 font-medium px-3">Now</th>' +
        '<th class="text-right pb-1 font-medium px-3">Min</th>' +
        '<th class="text-right pb-1 font-medium px-3">Avg</th>' +
        '<th class="text-right pb-1 font-medium pl-3">Max</th>' +
        '</tr></thead><tbody>' + rows + '</tbody></table>';
}
`
