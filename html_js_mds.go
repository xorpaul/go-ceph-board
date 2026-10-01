package main

const htmlJSMDS = `

function updateMDSDaemons(metrics) {
    const tbody = document.getElementById('mdsDaemonTableBody');
    if (!tbody) return;

    const metaArr = metrics['ceph_mds_metadata'] || [];
    if (metaArr.length === 0) {
        tbody.innerHTML = '<tr><td colspan="15" class="px-3 py-4 text-center text-gray-500 dark:text-gray-400">No MDS metadata found</td></tr>';
        return;
    }

    const fsNames = {};
    for (const fs of (metrics['ceph_fs_metadata'] || [])) {
        fsNames[fs.labels.fs_id] = fs.labels.name;
    }

    const daemonSessions = {};
    for (const m of (metrics['ceph_mds_sessions_session_count'] || [])) {
        daemonSessions[m.labels.ceph_daemon] = m.value;
    }

    const daemonInodes = {};
    for (const m of (metrics['ceph_mds_inodes_with_caps'] || [])) {
        daemonInodes[m.labels.ceph_daemon] = m.value;
    }

    const daemonSlowReply = {};
    for (const m of (metrics['ceph_mds_slow_reply'] || [])) {
        daemonSlowReply[m.labels.ceph_daemon] = m.value;
    }

    const daemonRequest = {};
    for (const m of (metrics['ceph_mds_request'] || [])) {
        daemonRequest[m.labels.ceph_daemon] = m.value;
    }

    const daemonDns = {};
    for (const m of (metrics['ceph_mds_mem_dn'] || [])) {
        daemonDns[m.labels.ceph_daemon] = m.value;
    }

    const daemonInosTotal = {};
    for (const m of (metrics['ceph_mds_inodes'] || [])) {
        daemonInosTotal[m.labels.ceph_daemon] = m.value;
    }

    const daemonDirs = {};
    for (const m of (metrics['ceph_mds_mem_dir'] || [])) {
        daemonDirs[m.labels.ceph_daemon] = m.value;
    }

    const daemonCaps = {};
    for (const m of (metrics['ceph_mds_mem_cap'] || [])) {
        daemonCaps[m.labels.ceph_daemon] = m.value;
    }

    const daemonRss = {};
    for (const m of (metrics['ceph_mds_mem_rss'] || [])) {
        daemonRss[m.labels.ceph_daemon] = m.value * 1024;
    }

    const daemonInodesExpired = {};
    for (const m of (metrics['ceph_mds_inodes_expired'] || [])) {
        daemonInodesExpired[m.labels.ceph_daemon] = m.value;
    }

    const hnSiteMap = buildHostSiteMap(metaArr);

    // Collect all ranked daemons (active, standby-replay, standby-passive)
    const daemons = [];
    for (const m of metaArr) {
        const rank = parseInt(m.labels.rank, 10);
        if (rank < 0) continue;
        const hostname   = m.labels.hostname || m.labels.ceph_daemon;
        const cephDaemon = m.labels.ceph_daemon;
        const fsId       = m.labels.fs_id;
        const state      = m.labels.state || '';
        const caps       = daemonCaps[cephDaemon] || 0;
        daemons.push({
            fsId,
            fsName:   fsNames[fsId] || ('fs#' + fsId),
            rank,
            cephDaemon,
            hostname,
            site:     hnSiteMap[hostname] || getSiteFromHostname(hostname, ''),
            sessions: daemonSessions[cephDaemon] || 0,
            caps,
            state,
        });
    }

    // Build stable per-hostname color map from sorted unique hostnames
    const allHostnames = [...new Set(daemons.map(d => d.hostname))].sort();
    const HOST_PALETTE = [
        '#f59e0b', // amber
        '#06b6d4', // cyan
        '#a78bfa', // violet
        '#f472b6', // pink
        '#34d399', // emerald
        '#fb923c', // orange
        '#818cf8', // indigo-light
        '#4ade80', // green
    ];
    const hostColorMap = {};
    allHostnames.forEach((hn, i) => { hostColorMap[hn] = HOST_PALETTE[i % HOST_PALETTE.length]; });

    const sortFn = (a, b) => {
        const fd = parseInt(a.fsId) - parseInt(b.fsId);
        if (fd !== 0) return fd;
        if (a.rank !== b.rank) return a.rank - b.rank;
        return a.hostname.localeCompare(b.hostname);
    };
    const activeDaemons        = daemons.filter(d => d.state === 'active').sort(sortFn);
    const standbyReplayDaemons = daemons.filter(d => d.state === 'standby-replay').sort(sortFn);
    const standbyDaemons       = daemons.filter(d => d.state !== 'active' && d.state !== 'standby-replay').sort(sortFn);

    tbody.innerHTML = '';

    function statCell(val) {
        return val > 0
            ? '<span class="text-xs font-mono text-gray-600 dark:text-gray-400" title="' + val.toLocaleString() + '">' + fmtCount(val) + '</span>'
            : '<span class="text-gray-400">—</span>';
    }

    function renderSection(group, label, bgClass) {
        if (group.length === 0) return;
        tbody.insertAdjacentHTML('beforeend',
            '<tr class="' + bgClass + '">' +
            '<td colspan="15" class="px-3 py-1 text-xs font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">' + label + '</td>' +
            '</tr>'
        );
        let lastFsId = null;
        for (const d of group) {
            const fsc = fsColor(d.fsId);
            const sc  = siteColor(d.site);
            const hc  = hostColorMap[d.hostname];
            const borderStyle = d.fsId !== lastFsId ? 'border-top:2px solid ' + fsc + ';' : '';
            lastFsId = d.fsId;

            const sessCell = d.sessions > 0
                ? '<span class="text-sm font-semibold text-gray-900 dark:text-white">' + d.sessions.toLocaleString() + '</span>'
                : '<span class="text-gray-400">—</span>';

            const inodesWithCaps = daemonInodes[d.cephDaemon] || 0;
            let inodesCell;
            if (inodesWithCaps === 0) {
                inodesCell = '<span class="text-gray-400">—</span>';
            } else {
                const inodesColor = inodesWithCaps >= 10e6 ? '#f97316' : inodesWithCaps >= 5e6 ? '#eab308' : '#6b7280';
                inodesCell = '<span class="text-xs font-mono font-semibold" style="color:' + inodesColor + '" title="' + inodesWithCaps.toLocaleString() + ' inodes with caps">' + fmtCount(inodesWithCaps) + '</span>';
            }

            const rates = calcMdsDaemonRates(d.cephDaemon, daemonSlowReply[d.cephDaemon] || 0, daemonRequest[d.cephDaemon] || 0);

            const reqCell = rates.reqRate < 0.5
                ? '<span class="text-gray-400">—</span>'
                : '<span class="text-xs font-mono text-gray-700 dark:text-gray-300">' + Math.round(rates.reqRate).toLocaleString() + '</span>';

            const slowCell = rates.slowReplyRate < 0.05
                ? '<span class="text-gray-400">—</span>'
                : '<span class="text-xs font-mono font-bold" style="color:#ef4444" title="MDS slow replies detected — cap pressure or memory stall">' + rates.slowReplyRate.toFixed(1) + '/s</span>';

            const rssBytes = daemonRss[d.cephDaemon];
            const rssCell = rssBytes !== undefined
                ? '<span class="text-xs font-mono text-gray-600 dark:text-gray-300">' + formatBytes(rssBytes) + '</span>'
                : '<span class="text-gray-400">—</span>';

            const inodesExpiredVal = daemonInodesExpired[d.cephDaemon];
            let trimCell;
            if (inodesExpiredVal === undefined) {
                trimCell = '<span class="text-gray-400">—</span>';
            } else {
                const trimRate = calcMdsTrimRate(d.cephDaemon, inodesExpiredVal);
                if (trimRate === null) {
                    trimCell = '<span class="text-gray-400">—</span>';
                } else {
                    const trimColor = trimRate >= 10000 ? '#ef4444' : trimRate >= 1000 ? '#f97316' : trimRate >= 1 ? '#eab308' : '#22c55e';
                    const trimText = trimRate < 1 ? '0' : Math.round(trimRate).toLocaleString();
                    trimCell = '<span class="text-xs font-mono font-semibold" style="color:' + trimColor + '">' + trimText + '/s</span>';
                }
            }

            tbody.insertAdjacentHTML('beforeend',
                '<tr class="hover:bg-gray-50 dark:hover:bg-gray-700" style="' + borderStyle + '">' +
                '<td class="px-3 py-2 text-sm font-semibold" style="color:' + fsc + ';border-left:3px solid ' + fsc + '">' + d.fsName + '</td>' +
                '<td class="px-3 py-2 text-sm text-right font-mono text-gray-500 dark:text-gray-400">' + d.rank + '</td>' +
                '<td class="px-3 py-2 text-xs font-mono text-gray-600 dark:text-gray-300">' + d.cephDaemon + '</td>' +
                '<td class="px-3 py-2 text-sm font-mono font-semibold" style="color:' + hc + '">' + d.hostname + '</td>' +
                '<td class="px-3 py-2"><span class="pg-state-badge" style="background:' + sc.bg + ';color:#fff">' + d.site + '</span></td>' +
                '<td class="px-3 py-2 text-right">' + sessCell + '</td>' +
                '<td class="px-3 py-2 text-right">' + reqCell + '</td>' +
                '<td class="px-3 py-2 text-right">' + statCell(daemonDns[d.cephDaemon] || 0) + '</td>' +
                '<td class="px-3 py-2 text-right">' + statCell(daemonInosTotal[d.cephDaemon] || 0) + '</td>' +
                '<td class="px-3 py-2 text-right">' + statCell(daemonDirs[d.cephDaemon] || 0) + '</td>' +
                '<td class="px-3 py-2 text-right">' + statCell(daemonCaps[d.cephDaemon] || 0) + '</td>' +
                '<td class="px-3 py-2 text-right">' + inodesCell + '</td>' +
                '<td class="px-3 py-2 text-right">' + slowCell + '</td>' +
                '<td class="px-3 py-2 text-right">' + rssCell + '</td>' +
                '<td class="px-3 py-2 text-right">' + trimCell + '</td>' +
                '</tr>'
            );
        }
    }

    renderSection(activeDaemons,        'Active',           'bg-gray-50 dark:bg-gray-800');
    renderSection(standbyReplayDaemons, 'Standby Replay',   'bg-gray-50 dark:bg-gray-800');
    renderSection(standbyDaemons,       'Standby / Passive', 'bg-gray-50 dark:bg-gray-800');
}

// ─── MDS Trim Rate by Filesystem ─────────────────────────────────────────────
function updateMdsTrimChart(metrics) {
    // ceph_daemon -> fs_id (active daemons only; standbys have fs_id="-1")
    const daemonFsId = {};
    for (const m of (metrics['ceph_mds_metadata'] || [])) {
        const fsId = m.labels.fs_id;
        if (fsId && fsId !== '-1') daemonFsId[m.labels.ceph_daemon] = fsId;
    }

    const fsNames = {};
    for (const fs of (metrics['ceph_fs_metadata'] || [])) {
        fsNames[fs.labels.fs_id] = fs.labels.name;
    }

    // Sum per-daemon trim rates by filesystem; also track per-daemon for modal charts
    const fsTrimRate = {};
    const daemonTrimRate = {};
    for (const m of (metrics['ceph_mds_inodes_expired'] || [])) {
        const fsId = daemonFsId[m.labels.ceph_daemon];
        if (!fsId) continue;
        const rate = calcMdsTrimRate(m.labels.ceph_daemon, m.value);
        if (rate === null) continue;
        fsTrimRate[fsId] = (fsTrimRate[fsId] || 0) + rate;
        daemonTrimRate[m.labels.ceph_daemon] = rate;
    }

    const now = new Date();
    pushHistoryLabel(mdsTrimChartHistory, now);

    // Ensure all known filesystems have a series; pad new entries to match label count
    const activeFsIds = new Set([
        ...Object.keys(fsTrimRate),
        ...Object.keys(mdsTrimChartHistory.byFs),
    ]);
    for (const fsId of activeFsIds) {
        if (!mdsTrimChartHistory.byFs[fsId]) {
            const padLen = Math.max(0, mdsTrimChartHistory.labels.length - 1);
            mdsTrimChartHistory.byFs[fsId] = {
                label: fsNames[fsId] || ('fs#' + fsId),
                color: fsColor(fsId),
                data: new Array(padLen).fill(null),
            };
        }
        pushHistory(mdsTrimChartHistory.byFs[fsId], fsTrimRate[fsId] ?? null);
    }

    // Per-daemon trim history for modal charts
    const knownDaemons = new Set([...Object.keys(daemonTrimRate), ...Object.keys(mdsTrimChartHistory.byDaemon)]);
    let daemonColorIdx = Object.keys(mdsTrimChartHistory.byDaemon).length;
    for (const daemon of knownDaemons) {
        if (!mdsTrimChartHistory.byDaemon[daemon]) {
            const padLen = Math.max(0, mdsTrimChartHistory.labels.length - 1);
            mdsTrimChartHistory.byDaemon[daemon] = {
                label: daemon.replace(/^mds\./, ''),
                color: PROM_QUERY_PALETTE[daemonColorIdx % PROM_QUERY_PALETTE.length],
                data: new Array(padLen).fill(null),
                fsId: daemonFsId[daemon] || null,
            };
            daemonColorIdx++;
        }
        pushHistory(mdsTrimChartHistory.byDaemon[daemon], daemonTrimRate[daemon] ?? null);
    }

    // Lazy-init the trim chart — canvas is static HTML but chart is destroyed on cluster switch
    const TRIM_THRESHOLDS = { warn: 50000, crit: 150000 };
    if (!charts.mdsTrim) charts.mdsTrim = makeTimeLineChart('mdsTrimChart', [], null, TRIM_THRESHOLDS);
    if (!charts.mdsTrim) return;
    // Show the section once we have at least one filesystem series
    const trimSection = document.getElementById('sec-mds-trim');
    if (trimSection) trimSection.classList.toggle('hidden', Object.keys(mdsTrimChartHistory.byFs).length === 0);

    // Populate stat cards: top filesystems sorted by current trim rate descending
    const trimGrid = document.getElementById('mdsTrimGrid');
    if (trimGrid) {
        const sorted = Object.entries(fsTrimRate).sort(([, a], [, b]) => b - a);
        trimGrid.innerHTML = sorted.map(([fsId, rate]) => {
            const label = fsNames[fsId] || ('fs#' + fsId);
            const color = fsColor(fsId);
            const valueColor = rate > 0 ? thresholdColor(rate, TRIM_THRESHOLDS) : color;
            const hex = color.replace('#', '');
            const r = parseInt(hex.slice(0,2),16), g = parseInt(hex.slice(2,4),16), b = parseInt(hex.slice(4,6),16);
            const bgStyle = 'background-color:rgba(' + r + ',' + g + ',' + b + ',0.12)';
            return '<div class="p-2 rounded-lg" style="' + bgStyle + '">' +
                '<p class="text-xs font-medium text-gray-500 dark:text-gray-400 truncate" title="' + escHtml(label) + '">' + escHtml(label) + '</p>' +
                '<p class="text-sm font-bold mt-0.5" style="color:' + valueColor + '">' + Math.round(rate).toLocaleString() + '/s</p>' +
                '</div>';
        }).join('');
    }

    charts.mdsTrim.data.labels = mdsTrimChartHistory.labels;
    charts.mdsTrim.data.datasets = Object.entries(mdsTrimChartHistory.byFs).map(([, h]) => ({
        label: h.label,
        data: h.data,
        borderColor: h.color,
        backgroundColor: h.color + '33',
        fill: false,
        tension: 0.4,
        pointRadius: 2,
        pointHoverRadius: 5,
        pointBackgroundColor: h.color,
        borderWidth: 1.5,
    }));
    charts.mdsTrim.update('none');

    updateChartLegend('mdsTrimLegend', Object.values(mdsTrimChartHistory.byFs).map(h => ({
        label: h.label,
        color: h.color,
        data: h.data.filter(v => v !== null),
        fmt: v => Math.round(v).toLocaleString() + '/s',
    })));
}

// ─── MDS Session Distribution Sankey ─────────────────────────────────────────
function escSvg(s) {
    return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function drawMDSSankey(metrics) {
    const container = document.getElementById('mdsSankeyContainer');
    if (!container) return;

    // Build per-(filesystem, host) session aggregation
    const fsNames = {};
    for (const fs of (metrics['ceph_fs_metadata'] || [])) fsNames[fs.labels.fs_id] = fs.labels.name;
    const daemonSessions = {};
    for (const m of (metrics['ceph_mds_sessions_session_count'] || [])) daemonSessions[m.labels.ceph_daemon] = m.value;

    const flowMap = {}; // fsId -> hn -> sessions
    for (const m of (metrics['ceph_mds_metadata'] || [])) {
        if (parseInt(m.labels.rank, 10) < 0) continue;
        const fsId = m.labels.fs_id;
        const hn   = m.labels.hostname || m.labels.ceph_daemon;
        const sess = daemonSessions[m.labels.ceph_daemon] || 0;
        if (!flowMap[fsId]) flowMap[fsId] = {};
        flowMap[fsId][hn] = (flowMap[fsId][hn] || 0) + sess;
    }

    // Aggregate totals; skip zero-session nodes
    const fsTotals = {}, hostTotals = {};
    for (const [fsId, hostMap] of Object.entries(flowMap)) {
        for (const [hn, sess] of Object.entries(hostMap)) {
            if (sess <= 0) continue;
            fsTotals[fsId] = (fsTotals[fsId] || 0) + sess;
            hostTotals[hn] = (hostTotals[hn]  || 0) + sess;
        }
    }
    const fsIds     = Object.keys(fsTotals).sort((a, b) => parseInt(a) - parseInt(b));
    const hostnames = Object.keys(hostTotals).sort();
    const totalSessions = Object.values(fsTotals).reduce((s, v) => s + v, 0);

    if (fsIds.length === 0 || totalSessions === 0) {
        container.innerHTML = '<p class="text-sm text-gray-400 px-2 py-4 text-center">No session data available.</p>';
        return;
    }

    const isDark    = document.documentElement.classList.contains('dark');
    const labelFill = isDark ? '#d1d5db' : '#374151';

    // Layout constants
    const W       = 700;
    const nodeW   = 12;
    const nodePad = 10;   // vertical gap between nodes
    const leftX   = 145;  // left edge of fs node column
    const rightX  = 555;  // left edge of host node column
    const flowCx  = (leftX + nodeW + rightX) / 2;  // bezier midpoint x

    // Node heights proportional to session totals; same scale for both sides
    const scale = 380 / totalSessions;
    const fsH = {}, hostH = {};
    fsIds.forEach(id     => { fsH[id]   = Math.max(6, fsTotals[id]   * scale); });
    hostnames.forEach(hn => { hostH[hn] = Math.max(6, hostTotals[hn] * scale); });

    // Column heights for centering
    const colHfs   = fsIds.reduce((s, id) => s + fsH[id], 0)      + nodePad * (fsIds.length - 1);
    const colHhost = hostnames.reduce((s, hn) => s + hostH[hn], 0) + nodePad * (hostnames.length - 1);
    const svgH     = Math.max(colHfs, colHhost) + 24;

    // Node y-positions (centered vertically within svgH)
    const fsY = {}, hostY = {};
    let y = (svgH - colHfs) / 2;
    for (const id of fsIds)     { fsY[id]   = y; y += fsH[id]   + nodePad; }
    y = (svgH - colHhost) / 2;
    for (const hn of hostnames) { hostY[hn] = y; y += hostH[hn] + nodePad; }

    // Stacking offsets within each node (flows are stacked in host-sorted order)
    const fsOff = {}; fsIds.forEach(id => fsOff[id] = 0);
    const hnOff = {}; hostnames.forEach(hn => hnOff[hn] = 0);

    // Build flow paths
    let paths = '';
    const x1 = leftX + nodeW, x2 = rightX;
    for (const fsId of fsIds) {
        for (const hn of hostnames) {
            const sess = (flowMap[fsId] && flowMap[fsId][hn]) || 0;
            if (sess <= 0) continue;
            const fh  = sess * scale;
            const y1a = fsY[fsId] + fsOff[fsId];
            const y2a = hostY[hn] + hnOff[hn];
            const y1b = y1a + fh, y2b = y2a + fh;
            fsOff[fsId] += fh;
            hnOff[hn]   += fh;
            const c     = fsColor(fsId);
            const tip   = escSvg((fsNames[fsId] || 'fs#'+fsId) + ' → ' + hn.split('.').slice(0,2).join('.') + ': ' + sess.toLocaleString() + ' sessions');
            paths += '<path d="M '+x1+' '+y1a+' C '+flowCx+' '+y1a+','+flowCx+' '+y2a+','+x2+' '+y2a+
                     ' L '+x2+' '+y2b+' C '+flowCx+' '+y2b+','+flowCx+' '+y1b+','+x1+' '+y1b+' Z"'+
                     ' fill="'+c+'" fill-opacity="0.35" stroke="'+c+'" stroke-opacity="0.55" stroke-width="0.5">'+
                     '<title>'+tip+'</title></path>\n';
        }
    }

    // Node rectangles + labels
    let nodes = '';
    for (const id of fsIds) {
        const c = fsColor(id), ny = fsY[id], nh = fsH[id];
        const name = escSvg(fsNames[id] || 'fs#'+id);
        nodes += '<rect x="'+leftX+'" y="'+ny+'" width="'+nodeW+'" height="'+nh+'" fill="'+c+'" rx="2"/>\n';
        if (nh >= 10) nodes += '<text x="'+(leftX-6)+'" y="'+(ny+nh/2)+'" text-anchor="end" dominant-baseline="middle" font-size="11" font-family="sans-serif" fill="'+labelFill+'">'+name+'</text>\n';
        nodes += '<text x="'+(leftX+nodeW+4)+'" y="'+(ny+nh/2)+'" dominant-baseline="middle" font-size="9" font-family="sans-serif" fill="'+c+'" font-weight="bold">'+fsTotals[id].toLocaleString()+'</text>\n';
    }
    const flowHnSiteMap = buildHostSiteMap(metrics['ceph_mds_metadata'] || []);
    for (const hn of hostnames) {
        const site = flowHnSiteMap[hn] || getSiteFromHostname(hn, '');
        const sc   = siteColor(site);
        const ny   = hostY[hn], nh = hostH[hn];
        const short = escSvg(hn.split('.').slice(0, 2).join('.'));
        nodes += '<rect x="'+rightX+'" y="'+ny+'" width="'+nodeW+'" height="'+nh+'" fill="'+sc.bg+'" rx="2"/>\n';
        if (nh >= 10) nodes += '<text x="'+(rightX+nodeW+6)+'" y="'+(ny+nh/2)+'" dominant-baseline="middle" font-size="11" font-family="sans-serif" fill="'+labelFill+'">'+short+'</text>\n';
        nodes += '<text x="'+(rightX-4)+'" y="'+(ny+nh/2)+'" text-anchor="end" dominant-baseline="middle" font-size="9" font-family="sans-serif" fill="'+sc.bg+'" font-weight="bold">'+hostTotals[hn].toLocaleString()+'</text>\n';
    }

    container.innerHTML = '<svg viewBox="0 0 '+W+' '+svgH+'" width="100%" style="display:block;overflow:visible">'+paths+nodes+'</svg>';
}

// ─── Pool sort state ──────────────────────────────────────────────────────────
let poolSortCol = 'pct';
let poolSortDir = -1; // 1 = asc, -1 = desc
let lastMetrics = null;

function setPoolSort(col) {
    poolSortDir = (poolSortCol === col) ? -poolSortDir : 1;
    poolSortCol = col;
    updatePoolSortIndicators();
    if (lastMetrics) updatePools(lastMetrics);
}

function updatePoolSortIndicators() {
    ['name','stored','overhead','used','avail','pct','rdIops','wrIops','rdMBps','wrMBps','objs'].forEach(function(c) {
        const el = document.getElementById('psort-' + c);
        if (!el) return;
        el.textContent = (c === poolSortCol) ? (poolSortDir === 1 ? ' ▲' : ' ▼') : '';
    });
}

function updatePools(metrics) {
    lastMetrics = metrics;
    const usedArr   = metrics['ceph_pool_bytes_used']    || [];
    const availArr  = metrics['ceph_pool_max_avail']     || [];
    const storedArr = metrics['ceph_pool_stored']        || [];
    const rdArr     = metrics['ceph_pool_rd']            || [];
    const wrArr     = metrics['ceph_pool_wr']            || [];
    const rdBArr    = metrics['ceph_pool_rd_bytes']      || [];
    const wrBArr    = metrics['ceph_pool_wr_bytes']      || [];
    const objArr    = metrics['ceph_pool_objects']        || [];
    const metaArr   = metrics['ceph_pool_metadata']      || [];

    // Index by pool_id
    function idxByPool(arr) {
        const m = {};
        for (const e of arr) { if (e.labels.pool_id !== undefined) m[e.labels.pool_id] = e; }
        return m;
    }
    const usedIdx   = idxByPool(usedArr);
    const availIdx  = idxByPool(availArr);
    const storedIdx = idxByPool(storedArr);
    // ceph_pool_metadata carries the human name; bytes_used/max_avail only have pool_id
    const metaIdx  = idxByPool(metaArr);
    const rdIdx   = idxByPool(rdArr);
    const wrIdx   = idxByPool(wrArr);
    const rdBIdx  = idxByPool(rdBArr);
    const wrBIdx  = idxByPool(wrBArr);
    const objIdx  = idxByPool(objArr);

    const poolIds = [...new Set([
        ...Object.keys(usedIdx), ...Object.keys(availIdx)
    ])];

    // Build pool objects for sorting
    const pools = poolIds.map(function(pid) {
        const usedE = usedIdx[pid] || {};
        const availE = availIdx[pid] || {};
        const meta  = metaIdx[pid]?.labels || {};
        const name  = meta.name || usedE.labels?.name || availE.labels?.name || 'pool-' + pid;
        const ptype = meta.type || '';
        const desc  = meta.description || '';
        const used   = usedE.value  || 0;
        const avail  = availE.value || 0;
        const stored = storedIdx[pid]?.value || 0;
        // overhead = raw OSD bytes / client data = USED / STORED from ceph df
        const overhead = stored > 0 ? used / stored : 0;
        const total = used + avail;
        const pct   = total > 0 ? (used / total * 100) : 0;
        const rd    = rdIdx[pid]?.value  || 0;
        const wr    = wrIdx[pid]?.value  || 0;
        const rdB   = rdBIdx[pid]?.value || 0;
        const wrB   = wrBIdx[pid]?.value || 0;
        const objs  = objIdx[pid]?.value || 0;
        const rates = calcPoolRates(pid, rd, wr, rdB, wrB);
        return { id: parseInt(pid), pid, name, ptype, desc, used, avail, stored, overhead, pct, objs,
                 rdIops: rates.rdIops, wrIops: rates.wrIops, rdMBps: rates.rdMBps, wrMBps: rates.wrMBps };
    });

    pools.sort(function(a, b) {
        const col = poolSortCol === 'id' ? 'id' : poolSortCol;
        const va = a[col], vb = b[col];
        if (typeof va === 'string') return poolSortDir * va.localeCompare(vb);
        return poolSortDir * (va - vb);
    });

    updatePoolSortIndicators();

    const tbody = document.getElementById('poolTableBody');
    tbody.innerHTML = '';

    for (const p of pools) {
        const dc = diskColor(p.pct);
        // Build type badge: "rep×4" for replicated, "ec k+m" for erasure
        let typeBadge = '';
        if (p.ptype === 'replicated') {
            const n = (p.desc.match(/replica:(\d+)/) || [])[1] || '?';
            typeBadge = '<span class="pg-state-badge" style="background:#14532d33;color:#4ade80;border:1px solid #14532d">rep\xD7' + n + '</span>';
        } else if (p.ptype === 'erasure') {
            const k = (p.desc.match(/k=(\d+)/) || [])[1] || '?';
            const m = (p.desc.match(/m=(\d+)/) || [])[1] || '?';
            typeBadge = '<span class="pg-state-badge" style="background:#1e3a5f33;color:#60a5fa;border:1px solid #1e3a5f">ec ' + k + '+' + m + '</span>';
        } else if (p.ptype) {
            typeBadge = '<span class="pg-state-badge" style="background:#37415133;color:#9ca3af">' + p.ptype + '</span>';
        }
        const overheadCell = p.overhead > 0
            ? '\xD7' + p.overhead.toFixed(2)
            : '<span class="text-gray-400">—</span>';
        tbody.insertAdjacentHTML('beforeend',
            '<tr class="hover:bg-gray-50 dark:hover:bg-gray-700">' +
            '<td class="px-3 py-2 text-sm font-medium text-gray-900 dark:text-white">' + p.name + '<span class="ml-1 text-xs text-gray-400">#' + p.pid + '</span></td>' +
            '<td class="px-3 py-2">' + typeBadge + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + (p.stored > 0 ? formatBytesHtml(p.stored) : '<span class="text-gray-400">—</span>') + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-700 dark:text-gray-200 font-semibold">' + overheadCell + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + formatBytesHtml(p.used) + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + formatBytesHtml(p.avail) + '</td>' +
            '<td class="px-3 py-2">' +
                '<div class="flex items-center gap-2">' +
                '<div class="disk-bar-bg flex-1"><div class="disk-bar-fill" style="width:' + Math.min(p.pct,100).toFixed(1) + '%;background:' + dc + '"></div></div>' +
                '<span class="text-xs font-mono font-bold w-10 text-right" style="color:' + dc + '">' + p.pct.toFixed(1) + '%</span>' +
                '</div>' +
            '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + p.rdIops + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + p.wrIops + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + p.rdMBps.toFixed(2) + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + p.wrMBps.toFixed(2) + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + p.objs.toLocaleString() + '</td>' +
            '</tr>'
        );
    }

    if (pools.length === 0) {
        tbody.innerHTML = '<tr><td colspan="12" class="px-3 py-6 text-center text-gray-500">No pool data found</td></tr>';
    }
}

`
