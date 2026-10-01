package main

const htmlJSMonitors = `
function updateOSDs(metrics) {
    const upArr  = metrics['ceph_osd_up']  || [];
    const inArr  = metrics['ceph_osd_in']  || [];
    const upCount  = upArr.filter(m => m.value === 1).length;
    const inCount  = inArr.filter(m => m.value === 1).length;
    const total    = upArr.length;

    document.getElementById('osdUp').textContent    = upCount;
    document.getElementById('osdIn').textContent    = inCount;
    document.getElementById('osdTotal').textContent = total;

    const now = new Date();
    pushHistoryLabel(osdHistory, now);
    pushHistory(osdHistory.up,    upCount);
    pushHistory(osdHistory.in,    inCount);
    pushHistory(osdHistory.total, total);
    updateChartData(charts.osd, osdHistory.labels);
    updateChartLegend('osdChartLegend', [
        {label: 'Up',    color: osdHistory.up.color,    data: osdHistory.up.data},
        {label: 'In',    color: osdHistory.in.color,    data: osdHistory.in.data},
        {label: 'Total', color: osdHistory.total.color, data: osdHistory.total.data},
    ]);
    pushSparkline(charts.osdSparkline, upCount);
}

function updatePGs(metrics) {
    const total        = sumVec(metrics, 'ceph_pg_total');
    const clean        = sumVec(metrics, 'ceph_pg_clean');
    const degraded     = sumVec(metrics, 'ceph_pg_degraded');
    const recovering   = sumVec(metrics, 'ceph_pg_recovering');
    const backfilling  = sumVec(metrics, 'ceph_pg_backfilling');
    const backfillWait = sumVec(metrics, 'ceph_pg_backfill_wait');
    const undersized   = sumVec(metrics, 'ceph_pg_undersized');

    document.getElementById('pgTotal').textContent      = total.toLocaleString();
    document.getElementById('pgClean').textContent      = clean.toLocaleString();
    document.getElementById('pgDegraded').textContent   = degraded.toLocaleString();
    document.getElementById('pgRecovering').textContent = recovering.toLocaleString();

    const bfRow = document.getElementById('pgBackfillingRow');
    bfRow.classList.toggle('hidden', backfilling === 0);
    document.getElementById('pgBackfilling').textContent = backfilling.toLocaleString();

    const bfwRow = document.getElementById('pgBackfillWaitRow');
    bfwRow.classList.toggle('hidden', backfillWait === 0);
    document.getElementById('pgBackfillWait').textContent = backfillWait.toLocaleString();

    // Backfill ETA: prefer tracking misplaced-object count (ceph_pg_objects_misplaced)
    // because a single large PG can stall PG-count-based ETA while objects drain.
    // Fall back to (backfilling + backfill_wait) PG count when the metric is absent.
    const pgPending       = backfilling + backfillWait;
    const misplacedObjs   = sumVec(metrics, 'ceph_pg_objects_misplaced', 0);
    const useObjCount     = misplacedObjs > 0;
    const etaPending      = useObjCount ? misplacedObjs : pgPending;
    const etaRow          = document.getElementById('pgBackfillEtaRow');
    const etaEl           = document.getElementById('pgBackfillEta');
    if (pgPending === 0) {
        backfillEtaHistory.length = 0;
        etaRow.classList.add('hidden');
    } else {
        const nowMs = Date.now();
        backfillEtaHistory.push({ ts: nowMs, pending: etaPending });
        // Drop entries older than 120 s
        while (backfillEtaHistory.length > 1 && nowMs - backfillEtaHistory[0].ts > BACKFILL_WINDOW_MS) {
            backfillEtaHistory.shift();
        }
        etaRow.classList.remove('hidden');
        if (backfillEtaHistory.length < 2) {
            etaEl.textContent = '…';
            etaEl.title = '';
        } else {
            const first   = backfillEtaHistory[0];
            const last    = backfillEtaHistory[backfillEtaHistory.length - 1];
            const elapsed = (last.ts - first.ts) / 1000;
            const rate    = (first.pending - last.pending) / elapsed; // units/s cleared
            if (rate <= 0) {
                etaEl.textContent = 'stalled';
                etaEl.title = useObjCount ? '0 obj/s' : '0 PG/s';
            } else {
                etaEl.textContent = '~' + fmtDuration(last.pending / rate);
                etaEl.title = useObjCount
                    ? Math.round(rate).toLocaleString() + ' obj/s'
                    : rate.toFixed(2) + ' PG/s';
            }
        }
    }

    const now = new Date();
    pushHistoryLabel(pgHistory, now);
    pushHistory(pgHistory.clean,        clean);
    pushHistory(pgHistory.degraded,     degraded);
    pushHistory(pgHistory.recovering,   recovering);
    pushHistory(pgHistory.backfilling,  backfilling);
    pushHistory(pgHistory.backfillWait, backfillWait);
    pushHistory(pgHistory.undersized,   undersized);
    updateChartData(charts.pg, pgHistory.labels);
    updateChartLegend('pgChartLegend', [
        {label: 'Clean',         color: pgHistory.clean.color,        data: pgHistory.clean.data},
        {label: 'Degraded',      color: pgHistory.degraded.color,     data: pgHistory.degraded.data},
        {label: 'Recovering',    color: pgHistory.recovering.color,   data: pgHistory.recovering.data},
        {label: 'Backfilling',   color: pgHistory.backfilling.color,  data: pgHistory.backfilling.data},
        {label: 'Backfill Wait', color: pgHistory.backfillWait.color, data: pgHistory.backfillWait.data},
        {label: 'Undersized',    color: pgHistory.undersized.color,   data: pgHistory.undersized.data},
    ]);
}

function updateMonitors(metrics, hostMetricsMap) {
    const quorumArr = metrics['ceph_mon_quorum_status']     || [];
    const metaArr   = metrics['ceph_mon_metadata']          || [];
    const sessArr   = metrics['ceph_mon_num_sessions']      || [];
    const ageArr    = metrics['ceph_mon_quorum_age']        || [];
    const skewArr   = metrics['ceph_mon_timecheck_skew']    || [];
    const latArr    = metrics['ceph_mon_timecheck_latency'] || [];
    const callArr   = metrics['ceph_mon_election_call']     || [];
    const winArr    = metrics['ceph_mon_election_win']      || [];
    const loseArr   = metrics['ceph_mon_election_lose']     || [];
    const storeArr  = metrics['ceph_mon_store_size']        || [];

    // Build a global IP→site map from OSD and MDS metadata (those hostnames carry site
    // markers like "site2").  Monitor hostnames are often short (e.g. "mon1") and contain
    // no site marker, so we fall back to this cross-cluster map via their public_addr IP.
    const ipSiteGlobal = {};
    for (const arr of [metrics['ceph_osd_metadata'] || [], metrics['ceph_mds_metadata'] || []]) {
        for (const m of arr) {
            const site = getSiteFromHostname(m.labels.hostname || m.labels.host || '', '');
            if (site === 'unknown') continue;
            const ip = extractIPFromAddr(m.labels.public_addr || '');
            if (ip) ipSiteGlobal[ip] = site;
        }
    }

    // build metadata map: daemon -> {hostname, ...}
    const monMeta = {};
    for (const m of metaArr) {
        const d = m.labels.ceph_daemon;
        if (d) monMeta[d] = m.labels;
    }

    // single value per daemon
    const byDaemon = (arr) => {
        const out = {};
        for (const e of arr) if (e.labels.ceph_daemon) out[e.labels.ceph_daemon] = e.value;
        return out;
    };
    // max absolute value per daemon (for skew/latency which have a peer label)
    const maxByDaemon = (arr) => {
        const out = {};
        for (const e of arr) {
            const d = e.labels.ceph_daemon;
            if (!d) continue;
            if (out[d] === undefined || Math.abs(e.value) > Math.abs(out[d])) out[d] = e.value;
        }
        return out;
    };

    const sessions  = byDaemon(sessArr);
    const ages      = byDaemon(ageArr);
    const skews     = maxByDaemon(skewArr);
    const latencies = maxByDaemon(latArr);
    const calls     = byDaemon(callArr);
    const wins      = byDaemon(winArr);
    const losses    = byDaemon(loseArr);
    const stores    = byDaemon(storeArr);

    const resolveMonSite = (meta, hostname) => {
        const crushStr = [meta.datacenter, meta.room, meta.rack].filter(Boolean).join(' ');
        let site = getSiteFromHostname(hostname, crushStr);
        if (site === 'unknown') {
            const ip = extractIPFromAddr(meta.public_addr || '');
            if (ip && ipSiteGlobal[ip]) site = ipSiteGlobal[ip];
        }
        return site;
    };

    // merge quorum status
    const mons = quorumArr.map(m => {
        const daemon   = m.labels.ceph_daemon;
        const meta     = monMeta[daemon] || {};
        const rawHn    = meta.hostname || daemon;
        const nm       = (hostMetricsMap && hostMetricsMap[rawHn]) || {};
        const hostname = nm._canonical || rawHn;
        const site     = resolveMonSite(meta, hostname);
        return { daemon, hostname, site, quorum: m.value === 1 };
    });

    // also pick up any in metadata not in quorum list
    for (const [d, meta] of Object.entries(monMeta)) {
        if (!mons.find(m => m.daemon === d)) {
            const rawHn    = meta.hostname || d;
            const nm       = (hostMetricsMap && hostMetricsMap[rawHn]) || {};
            const hostname = nm._canonical || rawHn;
            mons.push({ daemon: d, hostname, site: resolveMonSite(meta, hostname), quorum: false });
        }
    }

    mons.sort((a, b) => a.daemon.localeCompare(b.daemon));

    const majVer = majorityVersion(metaArr);
    const inQuorum = mons.filter(m => m.quorum).length;
    document.getElementById('monQuorum').textContent = inQuorum;
    document.getElementById('monTotal').textContent  = mons.length;

    // leader heuristic: most election wins is the best proxy for current Paxos leader;
    // sessions are not reliable (clients connect to any monitor, not just the leader).
    let leaderDaemon = null;
    const maxWins = Math.max(0, ...mons.map(m => wins[m.daemon] || 0));
    if (maxWins > 0) {
        leaderDaemon = mons.find(m => (wins[m.daemon] || 0) === maxWins)?.daemon || null;
    } else {
        // fallback when election metrics unavailable
        const maxSess = Math.max(0, ...mons.map(m => sessions[m.daemon] || 0));
        if (maxSess > 0) leaderDaemon = mons.find(m => (sessions[m.daemon] || 0) === maxSess)?.daemon || null;
    }

    // active ceph-mgr host from synthetic metric injected by the backend
    const activeMgrMeta = (metrics['go_ceph_active_mgr_host'] || [])[0];
    const activeMgrHost = activeMgrMeta ? activeMgrMeta.labels.hostname : null;

    const tbody = document.getElementById('monTableBody');
    tbody.innerHTML = '';
    for (const mon of mons) {
        const sc = siteColor(mon.site);
        const nm = (hostMetricsMap && hostMetricsMap[mon.hostname]) || {};

        const qBadge = mon.quorum
            ? '<span class="pg-state-badge" style="background:#065f46;color:#6ee7b7">IN QUORUM</span>'
            : '<span class="pg-state-badge" style="background:#7f1d1d;color:#fca5a5">OUT</span>';

        // sessions (plain — no longer used as leader proxy)
        const sessVal = sessions[mon.daemon];
        const sessHtml = sessVal === undefined
            ? '<span class="text-xs text-gray-400">—</span>'
            : '<span class="font-mono text-xs text-gray-700 dark:text-gray-300">' + sessVal + '</span>';

        // quorum age
        const ageVal = ages[mon.daemon];
        const ageHtml = ageVal === undefined
            ? '<span class="text-xs text-gray-400">—</span>'
            : '<span class="font-mono text-xs text-gray-700 dark:text-gray-300">' + formatDuration(ageVal) + '</span>';

        // clock skew (seconds → ms, max across peers)
        const skewVal = skews[mon.daemon];
        let skewHtml;
        if (skewVal === undefined) {
            skewHtml = '<span class="text-xs text-gray-400">—</span>';
        } else {
            const ms = Math.abs(skewVal) * 1000;
            const col = ms >= 50 ? '#ef4444' : ms >= 10 ? '#eab308' : '#22c55e';
            skewHtml = '<span class="font-mono text-xs font-bold" style="color:' + col + '">' + ms.toFixed(1) + 'ms</span>';
        }

        // peer round-trip latency (seconds → ms, max across peers)
        const latVal = latencies[mon.daemon];
        let latHtml;
        if (latVal === undefined) {
            latHtml = '<span class="text-xs text-gray-400">—</span>';
        } else {
            const ms = latVal * 1000;
            const col = latencyColor(ms);
            latHtml = '<span class="font-mono text-xs font-bold" style="color:' + col + '">' + ms.toFixed(1) + 'ms</span>';
        }

        // elections: wins / losses (total calls); ★ marks the probable Paxos leader (most wins)
        const winVal  = wins[mon.daemon];
        const loseVal = losses[mon.daemon];
        const callVal = calls[mon.daemon];
        const isLeader = mon.daemon === leaderDaemon;
        let electHtml;
        if (winVal === undefined && callVal === undefined) {
            electHtml = '<span class="text-xs text-gray-400">—</span>';
        } else {
            const wv = winVal  !== undefined ? winVal  : 0;
            const lv = loseVal !== undefined ? loseVal : 0;
            const cv = callVal !== undefined ? callVal : 0;
            const star = isLeader ? '<span style="color:#f59e0b;font-weight:700" title="Probable Paxos leader (most election wins)">&#9733; </span>' : '';
            electHtml = star +
                '<span class="font-mono text-xs font-bold" style="color:' + (isLeader ? '#f59e0b' : '#22c55e') + '">' + wv + 'W</span> ' +
                '<span class="font-mono text-xs font-bold" style="color:#ef4444">' + lv + 'L</span> ' +
                '<span class="font-mono text-xs text-gray-400">/' + cv + '</span>';
        }

        // RocksDB store size
        const storeVal = stores[mon.daemon];
        const storeHtml = storeVal === undefined
            ? '<span class="text-xs text-gray-400">—</span>'
            : '<span class="font-mono text-xs text-gray-700 dark:text-gray-300">' + formatBytes(storeVal) + '</span>';

        const isMgr = activeMgrHost && mon.hostname === activeMgrHost;
        const mgrBadge = isMgr
            ? ' <span class="pg-state-badge" style="background:#7c3aed;color:#fff" title="Active ceph-mgr is running on this host">MGR</span>'
            : '';
        const monVerFull = (monMeta[mon.daemon] || {}).ceph_version || null;
        tbody.insertAdjacentHTML('beforeend',
            '<tr class="hover:bg-gray-50 dark:hover:bg-gray-700">' +
            '<td class="px-3 py-2 text-sm font-mono text-gray-900 dark:text-white" style="border-left:3px solid ' + sc.color + '">' + mon.daemon + mgrBadge + '</td>' +
            '<td class="px-3 py-2 text-sm text-gray-600 dark:text-gray-300">' + mon.hostname + hostLinkHTML(mon.hostname) + '</td>' +
            '<td class="px-3 py-2"><span class="pg-state-badge" style="background:' + sc.bg + ';color:#fff">' + mon.site + '</span></td>' +
            '<td class="px-3 py-2">' + cephVersionCellHtml(monVerFull, majVer) + '</td>' +
            '<td class="px-3 py-2 text-center">' + qBadge + '</td>' +
            '<td class="px-3 py-2 text-center">' + sessHtml + '</td>' +
            '<td class="px-3 py-2">' + pctBarCellHtml(nm.cpuPct !== undefined ? nm.cpuPct : null, cpuColor, (nm.cpuCount ? nm.cpuCount + ' cores' + (nm.cpuModel ? '\n' + nm.cpuModel : '') : null), (nm.cpuCount ? nm.cpuCount + ' cores' : null)) + '</td>' +
            '<td class="px-3 py-2">' + (function() {
                const pct = nm.memPct !== undefined ? nm.memPct : null;
                if (pct === null) return '<span class="text-xs text-gray-400">—</span>';
                const color = diskColor(pct);
                const used  = nm.memTotal && nm.memAvail ? nm.memTotal - nm.memAvail : null;
                const tip   = used !== null ? formatBytes(used) + ' used / ' + formatBytes(nm.memAvail) + ' free' : '';
                return '<div class="flex items-center gap-1" title="' + tip + '">' +
                    '<div class="disk-bar-bg" style="width:48px"><div class="disk-bar-fill" style="width:' + Math.min(pct,100).toFixed(0) + '%;background:' + color + '"></div></div>' +
                    '<div><span class="text-xs font-mono font-bold" style="color:' + color + '">' + pct.toFixed(0) + '%</span>' +
                    (nm.memTotal ? '<br><span style="font-size:0.6rem" class="text-gray-400 dark:text-gray-500 font-mono">' + formatBytes(nm.memTotal) + '</span>' : '') +
                    '</div></div>';
            })() + '</td>' +
            '<td class="px-3 py-2 text-right">' + storeHtml + '</td>' +
            '<td class="px-3 py-2 text-center">' + ageHtml + '</td>' +
            '<td class="px-3 py-2 text-center">' + skewHtml + '</td>' +
            '<td class="px-3 py-2 text-center">' + latHtml + '</td>' +
            '<td class="px-3 py-2 text-center">' + electHtml + '</td>' +
            '</tr>'
        );
    }
}

// ─── MDS filesystem fullscreen modal ─────────────────────────────────────────
const _mdsModalData = {};  // fsId → {name, color, tblHtml, daemonList}
let _mdsModalOpen = null;  // currently open fsId, or null
let _mdsModalCharts = [];  // Chart.js instances created for the open modal

function openMdsFsModal(fsId) {
    const d = _mdsModalData[fsId];
    if (!d) return;
    _mdsModalOpen = fsId;
    const dark   = isDark();
    const modal  = document.getElementById('mds-fs-modal');
    const panel  = modal.firstElementChild;
    const header = document.getElementById('mds-fs-modal-header');
    const title  = document.getElementById('mds-fs-modal-title');
    const clust  = document.getElementById('mds-fs-modal-cluster');
    const body   = document.getElementById('mds-fs-modal-body');
    panel.style.background  = dark ? 'rgb(31,41,55)'  : '#fff';
    header.style.borderColor = dark ? 'rgba(255,255,255,0.1)' : '#e5e7eb';
    panel.querySelector('button').style.background = dark ? 'rgba(255,255,255,0.12)' : 'rgba(0,0,0,0.08)';
    panel.querySelector('button').style.color = dark ? '#e5e7eb' : '#374151';
    title.textContent = d.name;
    title.style.color = d.color;
    clust.textContent = (typeof currentCluster !== 'undefined' ? currentCluster : '');
    // Destroy any charts from a previous open before replacing body HTML
    for (const c of _mdsModalCharts) { try { c.destroy(); } catch(e) {} }
    _mdsModalCharts = [];

    body.innerHTML = d.tblHtml;
    const tbl = body.querySelector('table');
    if (tbl) {
        tbl.style.width    = '100%';
        tbl.style.fontSize = '15px';
        tbl.querySelectorAll('th, td').forEach(function(c) {
            c.style.padding = '4px 10px';
        });
    }

    // Inline sparklines per daemon in the Trim/s cells (modal-only, ~10 data points, no time axis)
    body.querySelectorAll('[data-trim-cell]').forEach(function(cell, idx) {
        const daemon = cell.getAttribute('data-trim-cell');
        const h = mdsTrimChartHistory.byDaemon[daemon];
        const color = (h && h.color) || PROM_QUERY_PALETTE[idx % PROM_QUERY_PALETTE.length];
        const skId = 'mds-ms-sk-' + idx;
        const prevHtml = cell.innerHTML;
        cell.innerHTML =
            '<div style="display:inline-flex;align-items:center;gap:4px;justify-content:flex-end">' +
            prevHtml +
            '<div style="width:55px;height:18px;flex-shrink:0"><canvas id="' + skId + '"></canvas></div>' +
            '</div>';
        const sk = makeSparkline(skId, color);
        if (sk) {
            if (h && h.data) {
                const recent = h.data.slice(-10);
                sk.data.labels = recent.map(function(_, i) { return i; });
                sk.data.datasets[0].data = recent;
                sk.update('none');
            }
            _mdsModalCharts.push(sk);
        }
    });

    // FS-aggregate Trim/s chart (large, full modal width)
    body.insertAdjacentHTML('beforeend',
        '<div style="margin-top:14px">' +
        '<p style="font-size:11px;font-weight:600;color:' + d.color + ';margin-bottom:4px">Trim/s aggregate (' + escHtml(d.name) + ')</p>' +
        '<div style="height:160px"><canvas id="mds-modal-fs-chart"></canvas></div>' +
        '</div>'
    );
    const fsH = mdsTrimChartHistory.byFs[fsId];
    const fsDatasets = fsH ? [{
        label: fsH.label,
        data: fsH.data,
        borderColor: d.color,
        backgroundColor: d.color + '33',
        fill: true, tension: 0.4, pointRadius: 2, borderWidth: 2,
    }] : [];
    const fsAggChart = makeTimeLineChart('mds-modal-fs-chart', fsDatasets);
    if (fsAggChart) {
        fsAggChart.data.labels = mdsTrimChartHistory.labels;
        fsAggChart.update('none');
        _mdsModalCharts.push(fsAggChart);
    }

    modal.hidden = false;
    document.addEventListener('keydown', _mdsModalKeyHandler);
}

function closeMdsFsModal() {
    _mdsModalOpen = null;
    for (const c of _mdsModalCharts) { try { c.destroy(); } catch(e) {} }
    _mdsModalCharts = [];
    const modal = document.getElementById('mds-fs-modal');
    if (modal) modal.hidden = true;
    document.removeEventListener('keydown', _mdsModalKeyHandler);
}

function _mdsModalKeyHandler(e) {
    if (e.key === 'Escape') closeMdsFsModal();
}

// Called from updateMDS after rebuilding _mdsModalData — refreshes modal if open
function _refreshMdsFsModal() {
    if (_mdsModalOpen && _mdsModalData[_mdsModalOpen]) openMdsFsModal(_mdsModalOpen);
}

function updateMDS(metrics, daemonStartTimeMap, rankAssignments, daemonMemLimitMap, srLagMap, journalLiveMap, srPresentMap) {
    daemonStartTimeMap = daemonStartTimeMap || {};
    rankAssignments    = rankAssignments    || {};
    daemonMemLimitMap  = daemonMemLimitMap  || {};
    srLagMap           = srLagMap           || {};
    journalLiveMap     = journalLiveMap     || {};
    srPresentMap       = srPresentMap       || {};
    const metaArr   = metrics['ceph_mds_metadata'] || [];
    const fsMetaArr = metrics['ceph_fs_metadata']  || [];
    const tbody     = document.getElementById('mdsTableBody');

    if (metaArr.length === 0) {
        tbody.innerHTML = '<tr><td colspan="6" class="px-3 py-4 text-center text-gray-500 dark:text-gray-400">No MDS metadata found</td></tr>';
        return;
    }

    // fs_id → name  (e.g. "1" → "fs1")
    const fsNames = {};
    for (const fs of fsMetaArr) {
        fsNames[fs.labels.fs_id] = fs.labels.name;
    }

    // ceph_daemon → session count
    const daemonSessions = {};
    for (const m of (metrics['ceph_mds_sessions_session_count'] || [])) {
        daemonSessions[m.labels.ceph_daemon] = m.value;
    }

    // Daemon stat lookups for the per-FS detail grid
    const daemonDns  = {}; for (const m of (metrics['ceph_mds_mem_dn']  || [])) daemonDns[m.labels.ceph_daemon]  = m.value;
    const daemonInos = {}; for (const m of (metrics['ceph_mds_inodes']  || [])) daemonInos[m.labels.ceph_daemon] = m.value;
    const daemonDirs = {}; for (const m of (metrics['ceph_mds_mem_dir'] || [])) daemonDirs[m.labels.ceph_daemon] = m.value;
    const daemonCaps = {}; for (const m of (metrics['ceph_mds_mem_cap'] || [])) daemonCaps[m.labels.ceph_daemon] = m.value;
    const daemonSlowReply = {}; for (const m of (metrics['ceph_mds_slow_reply'] || [])) daemonSlowReply[m.labels.ceph_daemon] = m.value;
    const daemonRequest   = {}; for (const m of (metrics['ceph_mds_request']    || [])) daemonRequest[m.labels.ceph_daemon]   = m.value;
    // RSS in KB → bytes
    const daemonRss = {}; for (const m of (metrics['ceph_mds_mem_rss'] || [])) daemonRss[m.labels.ceph_daemon] = m.value * 1024;
    const daemonCapRevoke = {}; for (const m of (metrics['ceph_mds_ceph_cap_op_revoke'] || [])) daemonCapRevoke[m.labels.ceph_daemon] = m.value;
    const daemonInodesExpired = {}; for (const m of (metrics['ceph_mds_inodes_expired'] || [])) daemonInodesExpired[m.labels.ceph_daemon] = m.value;
    const daemonCapThrottle  = {}; for (const m of (metrics['ceph_mds_server_cap_acquisition_throttle'] || [])) daemonCapThrottle[m.labels.ceph_daemon]  = m.value;
    const daemonCapEviction  = {}; for (const m of (metrics['ceph_mds_server_cap_revoke_eviction']     || [])) daemonCapEviction[m.labels.ceph_daemon]   = m.value;

    // Sort daemons into active/standby-replay (rank ≥ 0) and standby (rank = -1)
    const activeByFs = {};   // fs_id → [{rank, hostname, site, cephDaemon, state}]
    const standbyList = [];  // [{hostname, site}]

    // Pre-build hostname→site using IP cross-referencing so that bare short hostnames
    // (e.g. "mds1") resolve correctly when a qualified peer (e.g. "mds1.site2") shares
    // the same public_addr IP and provides the site label.
    const hostSiteMap = buildHostSiteMap(metaArr);

    for (const m of metaArr) {
        const hostname   = m.labels.hostname || m.labels.ceph_daemon;
        const rank       = parseInt(m.labels.rank, 10);
        const fsId       = m.labels.fs_id;
        const site       = hostSiteMap[hostname] || getSiteFromHostname(hostname, '');
        const cephDaemon = m.labels.ceph_daemon;
        const state      = m.labels.state || '';

        if (rank >= 0) {
            if (!activeByFs[fsId]) activeByFs[fsId] = [];
            activeByFs[fsId].push({ rank, hostname, site, cephDaemon, state });
        } else {
            standbyList.push({ hostname, site });
        }
    }

    tbody.innerHTML = '';

    // ── MDS active site indicator ─────────────────────────────────────────────
    const strip      = document.getElementById('mdsActiveSiteStrip');
    const pillsEl    = document.getElementById('mdsActiveSitePills');
    const daemonsEl  = document.getElementById('mdsActiveSiteDaemons');
    const allFsIds   = Object.keys(activeByFs).sort((a, b) => parseInt(a) - parseInt(b));
    // Filter strip by production list when not showing all.
    const fsIds = showAllMDS ? allFsIds : allFsIds.filter(id => isProductionFS(fsNames[id] || ''));
    pillsEl.innerHTML = '';
    for (const fsId of fsIds) {
        const fsName   = fsNames[fsId] || ('FS #' + fsId);
        const active   = activeByFs[fsId].sort((a, b) => a.rank - b.rank);
        const rank0    = active.find(a => (daemonCaps[a.cephDaemon] || 0) > 0 && a.rank === 0);
        const fsc      = fsColor(fsId);
        let html       = '<span class="flex items-center gap-1.5 text-xs">';
        html          += '<span class="font-bold" style="color:' + fsc + '">' + fsName + '</span>';
        html          += '<span class="text-gray-400 dark:text-gray-500">rank 0 →</span>';
        if (rank0) {
            const sc = siteColor(rank0.site);
            html += '<span class="pg-state-badge mds-site-primary" style="background:' + sc.bg + ';color:#fff" title="Site holding MDS rank 0 for ' + fsName + '">' + rank0.site + ' ★</span>';
        } else {
            html += '<span class="text-gray-400">—</span>';
        }
        // Show other active (non-replay) sites at a glance (without ★)
        const otherSites = [...new Set(active.filter(a => (daemonCaps[a.cephDaemon] || 0) > 0 && a.rank !== 0).map(a => a.site))];
        for (const site of otherSites) {
            if (rank0 && site === rank0.site) continue;
            const sc = siteColor(site);
            html += '<span class="pg-state-badge" style="background:' + sc.bg + '88;color:#fff">' + site + '</span>';
        }
        html += '</span>';
        pillsEl.insertAdjacentHTML('beforeend', html);
        if (fsId !== fsIds[fsIds.length - 1]) {
            pillsEl.insertAdjacentHTML('beforeend', '<span class="text-gray-300 dark:text-gray-600 select-none">|</span>');
        }
    }
    strip.classList.toggle('hidden', fsIds.length === 0);

    // ── Per-FS daemon detail grid ─────────────────────────────────────────────
    // Shows active + standby-replay daemons for each FS in a compact table.
    // Font is intentionally tiny to fit many ranks without scrolling.
    if (daemonsEl) {
        const dark = isDark();
        const thTxt  = dark ? '#9ca3af' : '#6b7280';
        const txtMain = dark ? '#d1d5db' : '#374151';
        const txtStat = dark ? '#9ca3af' : '#6b7280';
        const txtFaint = dark ? '#6b7280' : '#9ca3af';
        const cardBorder = dark ? '#374151' : '#e5e7eb';
        const sepBorder = dark ? '#374151' : '#e5e7eb';
        const headBorder = dark ? '#4b5563' : '#e5e7eb';

        const now = Date.now() / 1000;
        const sinceStr = epochSec => {
            if (!epochSec) return '—';
            const s = now - epochSec;
            if (s < 120)      return Math.round(s) + 's';
            if (s < 7200)     return Math.round(s / 60) + 'm';
            if (s < 172800)   return (s / 3600).toFixed(1) + 'h';
            return Math.floor(s / 86400) + 'd ' + Math.floor((s % 86400) / 3600) + 'h';
        };

        let gridHtml = '<div class="flex flex-wrap gap-3 mt-1">';
        for (const fsId of fsIds) {
            const fsName = fsNames[fsId] || ('FS #' + fsId);
            const fsc    = fsColor(fsId);
            const daemons = (activeByFs[fsId] || []).slice().sort((a, b) => {
                // active (CAPS > 0) before standby-replay; within group by rank asc
                const aReplay = (daemonCaps[a.cephDaemon] || 0) > 0 ? 0 : 1;
                const bReplay = (daemonCaps[b.cephDaemon] || 0) > 0 ? 0 : 1;
                if (aReplay !== bReplay) return aReplay - bReplay;
                return a.rank - b.rank;
            });

            // Totals: Reqs/s DNS INOS DIRS CAPS Recall/s Trim from active daemons; Mem from all daemons
            let totalReqs = 0, totalDns = 0, totalInos = 0, totalDirs = 0, totalCaps = 0, totalMem = 0, totalTrim = 0, totalRecall = 0, totalThrottle = 0, totalEviction = 0;
            let hasMem = false, hasTrim = false, hasRecall = false, hasThrottle = false, hasEviction = false;
            let totalJrnlLive = 0, hasJrnlLive = false;
            let totalMaxLag = undefined, smallestMargin = undefined, smallestMarginDaemon = null;
            for (const d of daemons) {
                const isActive = (daemonCaps[d.cephDaemon] || 0) > 0;
                if (isActive) {
                    const r = calcMdsDaemonRates(d.cephDaemon, daemonSlowReply[d.cephDaemon] || 0, daemonRequest[d.cephDaemon] || 0);
                    if (r && r.reqRate >= 0.5) totalReqs += r.reqRate;
                    totalDns  += daemonDns[d.cephDaemon]  || 0;
                    totalInos += daemonInos[d.cephDaemon] || 0;
                    totalDirs += daemonDirs[d.cephDaemon] || 0;
                    totalCaps += daemonCaps[d.cephDaemon] || 0;
                    if (daemonInodesExpired[d.cephDaemon] !== undefined) {
                        const tr = calcMdsTrimRate(d.cephDaemon, daemonInodesExpired[d.cephDaemon]);
                        if (tr !== null) { totalTrim += tr; hasTrim = true; }
                    }
                    if (daemonCapRevoke[d.cephDaemon] !== undefined) {
                        const rr = calcMdsRecallRate(d.cephDaemon, daemonCapRevoke[d.cephDaemon]);
                        if (rr !== null) { totalRecall += rr; hasRecall = true; }
                    }
                    if (daemonCapThrottle[d.cephDaemon] !== undefined) { totalThrottle += daemonCapThrottle[d.cephDaemon]; hasThrottle = true; }
                    if (daemonCapEviction[d.cephDaemon] !== undefined) { totalEviction += daemonCapEviction[d.cephDaemon]; hasEviction = true; }
                    const liveKey = fsName + '/' + d.rank;
                    const live = journalLiveMap[liveKey];
                    if (live !== undefined) { totalJrnlLive += live; hasJrnlLive = true; }
                } else {
                    const srEntry = srLagMap[d.cephDaemon];
                    if (srEntry) {
                        if (srEntry.lag !== undefined) {
                            totalMaxLag = totalMaxLag === undefined ? srEntry.lag : Math.max(totalMaxLag, srEntry.lag);
                        }
                        if (srEntry.margin !== undefined) {
                            if (smallestMargin === undefined || srEntry.margin < smallestMargin) {
                                smallestMargin = srEntry.margin;
                                smallestMarginDaemon = d.cephDaemon;
                            }
                        }
                    }
                }
                if (daemonRss[d.cephDaemon] !== undefined) { totalMem += daemonRss[d.cephDaemon]; hasMem = true; }
            }
            const totalTrimStr = !hasTrim ? '—' : (totalTrim < 1 ? '0/s' : Math.round(totalTrim).toLocaleString() + '/s');
            const totalRecallStr = !hasRecall ? '—' : (totalRecall < 1 ? '0/s' : Math.round(totalRecall).toLocaleString() + '/s');
            const totalMemStr = hasMem ? formatBytes(totalMem) : '—';
            const totRowSt = 'padding:1px 4px;text-align:right;font-family:monospace;font-weight:600;color:' + txtStat;

            let tbl = '<table style="font-size:10px;border-collapse:collapse;white-space:nowrap">' +
                '<thead>' +
                '<tr style="color:' + thTxt + ';border-bottom:1px solid ' + headBorder + '">' +
                '<th style="padding:1px 4px;text-align:right">Rank</th>' +
                '<th style="padding:1px 4px;text-align:left">Daemon</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Requests/s (active) — not available for standby-replay">Reqs/s</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Dentries in cache (ceph_mds_mem_dn)">DNS</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Inodes in cache (ceph_mds_inodes)">INOS</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Directories in cache (ceph_mds_mem_dir)">DIRS</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Caps issued (ceph_mds_mem_cap)">CAPS</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Cap recall rate — delta of ceph_mds_ceph_cap_op_revoke/s. Non-zero means the MDS is actively revoking caps from clients.">Recall/s</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Cap acquisition throttle count since daemon start (ceph_mds_server_cap_acquisition_throttle) — non-zero means memory pressure caused the MDS to slow new cap grants">Thrtl</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Client eviction count since daemon start (ceph_mds_server_cap_revoke_eviction) — any non-zero value means clients were force-disconnected for failing to return caps">Evict</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Resident memory (ceph_mds_mem_rss); % of mds_cache_memory_limit shown when available (queried per daemon via ceph tell)">Mem</th>' +
                '<th style="padding:1px 4px;text-align:right" title="LRU inode eviction rate (delta of ceph_mds_inodes_expired/s, cache inodes, not journal). Non-zero means the MDS is trimming cold inodes to stay within mds_cache_memory_limit.">Trim/s</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Active: untrimmed journal (wrpos - expos). Standby-replay: bytes it can still fall behind before the active trims journal it has not replayed (rdpos - active expos); <= 0 means it respawns with fell behind journal.">Jrnl</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Journal written by the active but not yet replayed by this standby-replay (wrpos - rdpos). A few KB is normal; steady growth means it cannot keep up.">Lag</th>' +
                '<th style="padding:1px 4px;text-align:right" title="Time since this daemon was assigned its current rank (tracked by go-ceph-board). Falls back to systemd service start time when no rank-change has been observed yet. Shows \'—\' when neither source is available.">Since</th>' +
                '</tr>' +
                '<tr style="border-bottom:1px solid ' + headBorder + ';background:' + (isDark() ? 'rgba(255,255,255,0.03)' : 'rgba(0,0,0,0.03)') + '">' +
                '<td style="padding:1px 4px;text-align:right;color:' + txtFaint + ';font-size:9px">∑</td>' +
                '<td style="padding:1px 4px;text-align:left;color:' + txtFaint + ';font-size:9px">active / all</td>' +
                '<td style="' + totRowSt + '" title="Total Reqs/s across active daemons">' + (totalReqs > 0 ? Math.round(totalReqs).toLocaleString() : '—') + '</td>' +
                '<td style="' + totRowSt + '" title="Total DNS across active daemons">' + fmtCount(totalDns) + '</td>' +
                '<td style="' + totRowSt + '" title="Total INOS across active daemons">' + fmtCount(totalInos) + '</td>' +
                '<td style="' + totRowSt + '" title="Total DIRS across active daemons">' + fmtCount(totalDirs) + '</td>' +
                '<td style="' + totRowSt + '" title="Total CAPS across active daemons">' + fmtCount(totalCaps) + '</td>' +
                '<td style="' + totRowSt + '" title="Total cap recall rate across active daemons">' + totalRecallStr + '</td>' +
                '<td style="' + totRowSt + '" title="Total cap acquisition throttle count across active daemons">' + (!hasThrottle ? '—' : (totalThrottle > 0 ? '<span style="color:#eab308">' + totalThrottle.toLocaleString() + '</span>' : '<span style="color:#22c55e">0</span>')) + '</td>' +
                '<td style="' + totRowSt + '" title="Total client eviction count across active daemons">' + (!hasEviction ? '—' : (totalEviction > 0 ? '<span style="color:#ef4444">' + totalEviction.toLocaleString() + '</span>' : '<span style="color:#22c55e">0</span>')) + '</td>' +
                '<td style="' + totRowSt + '" title="Total Mem across all daemons (active + standby-replay)">' + totalMemStr + '</td>' +
                '<td style="' + totRowSt + '" title="Total trim rate across active daemons">' + totalTrimStr + '</td>' +
                '<td style="' + totRowSt + '" title="Sum of active journal sizes">' + (hasJrnlLive ? formatBytes(totalJrnlLive) : '—') + '</td>' +
                '<td style="' + totRowSt + '" title="Max SR lag' + (smallestMarginDaemon ? '; smallest margin: ' + smallestMarginDaemon.replace(/^mds\./, '') : '') + '">' + (totalMaxLag !== undefined ? formatBytes(totalMaxLag) : '—') + '</td>' +
                '<td></td>' +
                '</tr>' +
                '</thead><tbody>';

            let lastIsReplay = false;
            for (const d of daemons) {
                const isReplay = (daemonCaps[d.cephDaemon] || 0) === 0;
                const rankLabel = isReplay ? d.rank + '-s' : String(d.rank);
                const shortDaemon = d.cephDaemon.replace(/^mds\./, '');
                const rates = isReplay ? null : calcMdsDaemonRates(d.cephDaemon, daemonSlowReply[d.cephDaemon] || 0, daemonRequest[d.cephDaemon] || 0);
                const reqStr = (rates && rates.reqRate >= 0.5) ? Math.round(rates.reqRate).toLocaleString() : '—';
                const memBytes = daemonRss[d.cephDaemon];
                const memLimit = daemonMemLimitMap[d.cephDaemon];
                let memStr;
                if (memBytes === undefined) {
                    memStr = '—';
                } else if (memLimit > 0) {
                    const pct = memBytes / memLimit * 100;
                    const memColor = pct >= 100 ? '#ef4444' : pct >= 90 ? '#f97316' : '#22c55e';
                    memStr = '<span style="color:' + memColor + '" title="mds_cache_memory_limit: ' + formatBytes(memLimit) + '">' + formatBytes(memBytes) + ' (' + Math.round(pct) + '%)</span>';
                } else {
                    memStr = formatBytes(memBytes);
                }
                const rankEntry   = rankAssignments[shortDaemon];
                const sinceEpoch  = (rankEntry && rankEntry.assigned_at) ? rankEntry.assigned_at : daemonStartTimeMap[d.cephDaemon];
                const sStr = sinceStr(sinceEpoch);
                const recentStart = sinceEpoch && (now - sinceEpoch) < 3600;

                if (isReplay && !lastIsReplay) {
                    tbl += '<tr><td colspan="15" style="padding:1px 4px;color:' + txtFaint + ';font-style:italic;border-top:1px dotted ' + sepBorder + '">standby-replay</td></tr>';
                }
                lastIsReplay = isReplay;

                let rowBg;
                if (recentStart) {
                    rowBg = 'background:rgba(239,68,68,0.15)';
                } else if (isReplay) {
                    rowBg = 'background:rgba(99,102,241,0.08)';
                } else {
                    rowBg = 'background:rgba(34,197,94,0.08)';
                }
                const rankColor = isReplay ? '#6366f1' : fsc;
                const sc = siteColor(d.site);
                const siteDot = '<span style="display:inline-block;width:6px;height:6px;border-radius:50%;background:' + sc.bg + ';vertical-align:middle;margin-right:3px" title="' + d.site + '"></span>';

                tbl += '<tr style="' + rowBg + '">' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:' + rankColor + ';font-weight:600">' + rankLabel + '</td>' +
                    '<td style="padding:1px 4px;font-family:monospace;color:' + txtMain + '">' + siteDot + shortDaemon + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:' + txtStat + '">' + reqStr + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:' + txtStat + '">' + (daemonDns[d.cephDaemon] !== undefined  ? fmtCount(daemonDns[d.cephDaemon])  : '—') + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:' + txtStat + '">' + (daemonInos[d.cephDaemon] !== undefined ? fmtCount(daemonInos[d.cephDaemon]) : '—') + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:' + txtStat + '">' + (daemonDirs[d.cephDaemon] !== undefined ? fmtCount(daemonDirs[d.cephDaemon]) : '—') + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:' + txtStat + '">' + (daemonCaps[d.cephDaemon] !== undefined ? fmtCount(daemonCaps[d.cephDaemon]) : '—') + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace">' + (() => {
                        if (isReplay) return '<span style="color:' + txtFaint + '">—</span>';
                        const rv = daemonCapRevoke[d.cephDaemon];
                        if (rv === undefined) return '<span style="color:' + txtFaint + '">—</span>';
                        const rr = calcMdsRecallRate(d.cephDaemon, rv);
                        if (rr === null) return '<span style="color:' + txtFaint + '">—</span>';
                        const rc = rr >= 1000 ? '#ef4444' : rr >= 100 ? '#f97316' : rr >= 1 ? '#eab308' : '#22c55e';
                        return '<span style="color:' + rc + ';font-weight:600">' + (rr < 1 ? '0' : Math.round(rr).toLocaleString()) + '/s</span>';
                    })() + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace">' + (() => {
                        const tv = daemonCapThrottle[d.cephDaemon];
                        if (tv === undefined) return '<span style="color:' + txtFaint + '">—</span>';
                        const tc = tv > 0 ? '#eab308' : '#22c55e';
                        return '<span style="color:' + tc + ';font-weight:' + (tv > 0 ? '700' : 'normal') + '">' + tv.toLocaleString() + '</span>';
                    })() + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace">' + (() => {
                        const ev = daemonCapEviction[d.cephDaemon];
                        if (ev === undefined) return '<span style="color:' + txtFaint + '">—</span>';
                        const ec = ev > 0 ? '#ef4444' : '#22c55e';
                        return '<span style="color:' + ec + ';font-weight:' + (ev > 0 ? '700' : 'normal') + '">' + ev.toLocaleString() + '</span>';
                    })() + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:' + txtStat + '">' + memStr + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace" data-trim-cell="' + escHtml(d.cephDaemon) + '">' + (() => {
                        const ev = daemonInodesExpired[d.cephDaemon];
                        if (ev === undefined) return '<span style="color:' + txtFaint + '">—</span>';
                        const tr = calcMdsTrimRate(d.cephDaemon, ev);
                        if (tr === null) return '<span style="color:' + txtFaint + '">—</span>';
                        const tc = tr >= 10000 ? '#ef4444' : tr >= 1000 ? '#f97316' : tr >= 1 ? '#eab308' : '#22c55e';
                        return '<span style="color:' + tc + ';font-weight:600">' + (tr < 1 ? '0' : Math.round(tr).toLocaleString()) + '/s</span>';
                    })() + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace">' + (() => {
                        if (!isReplay) {
                            const live = journalLiveMap[fsName + '/' + d.rank];
                            if (live === undefined) return '<span style="color:' + txtFaint + '">—</span>';
                            return '<span style="color:' + txtStat + '">' + formatBytes(live) + '</span>';
                        }
                        const srEntry = srLagMap[d.cephDaemon];
                        const margin = srEntry ? srEntry.margin : undefined;
                        if (margin === undefined) return '<span style="color:' + txtFaint + '">—</span>';
                        const live = journalLiveMap[fsName + '/' + d.rank];
                        const pct = (live !== undefined && live > 0) ? (margin / live * 100) : null;
                        let color;
                        if (margin <= 0) {
                            color = '#ef4444';
                        } else if (pct !== null) {
                            color = pct >= 50 ? '#22c55e' : pct >= 20 ? '#f97316' : '#ef4444';
                        } else {
                            color = '#22c55e';
                        }
                        const absMargin = Math.abs(margin);
                        const prefix = margin < 0 ? '-' : '';
                        const pctStr = pct !== null ? ' · ' + Math.min(100, Math.max(0, Math.round(pct))) + '%' : '';
                        return '<span style="color:' + color + ';font-weight:600">' + prefix + formatBytes(absMargin) + pctStr + '</span>';
                    })() + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace">' + (() => {
                        if (!isReplay) return '<span style="color:' + txtFaint + '">—</span>';
                        const srEntry = srLagMap[d.cephDaemon];
                        const lag = srEntry ? srEntry.lag : undefined;
                        if (lag === undefined) return '<span style="color:' + txtFaint + '">—</span>';
                        const growing = checkSrLagTrend(d.cephDaemon, lag);
                        let color;
                        if ((growing && lag >= 1e6) || lag >= 10e6) {
                            color = '#ef4444';
                        } else if (lag >= 1e6) {
                            color = '#eab308';
                        } else {
                            color = '#22c55e';
                        }
                        return '<span style="color:' + color + ';font-weight:600">' + formatBytes(lag) + '</span>';
                    })() + '</td>' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:' + (recentStart ? '#ef4444' : txtFaint) + ';font-weight:' + (recentStart ? '700' : 'normal') + '" title="' + (sinceEpoch ? new Date(sinceEpoch * 1000).toLocaleString() : 'no data') + (isReplay && recentStart ? '\nRespawned or newly assigned; check journalctl on this host for fell behind journal.' : '') + '">' + sStr + '</td>' +
                    '</tr>';
            }

            // Emit "no standby-replay" placeholder rows for ranks confirmed SR-less by the script.
            const srRanks = new Set(daemons.filter(d => (daemonCaps[d.cephDaemon] || 0) === 0).map(d => d.rank));
            const activeRanksArr = daemons.filter(d => (daemonCaps[d.cephDaemon] || 0) > 0).map(d => d.rank).sort((a, b) => a - b);
            for (const rank of activeRanksArr) {
                if (srRanks.has(rank)) continue;
                const presentKey = fsName + '/' + String(rank);
                if (srPresentMap[presentKey] !== 0) continue;
                if (!lastIsReplay) {
                    tbl += '<tr><td colspan="15" style="padding:1px 4px;color:' + txtFaint + ';font-style:italic;border-top:1px dotted ' + sepBorder + '">standby-replay</td></tr>';
                    lastIsReplay = true;
                }
                tbl += '<tr style="background:rgba(239,68,68,0.06)">' +
                    '<td style="padding:1px 4px;text-align:right;font-family:monospace;color:#ef4444;font-weight:600">' + rank + '-s</td>' +
                    '<td style="padding:1px 4px;font-family:monospace;color:' + txtFaint + ';font-style:italic" colspan="14">(no standby-replay)</td>' +
                    '</tr>';
            }

            tbl += '</tbody></table>';

            // Store for modal (live-update on next refresh)
            const activeDaemonList = daemons.filter(d => (daemonCaps[d.cephDaemon] || 0) > 0).map(d => d.cephDaemon);
            _mdsModalData[fsId] = { name: fsName, color: fsc, tblHtml: tbl, daemonList: activeDaemonList };

            const expandBtn =
                '<button onclick="openMdsFsModal(' + escHtml(JSON.stringify(fsId)) + ')" ' +
                'title="Open fullscreen" ' +
                'style="padding:1px 5px;border-radius:4px;border:1px solid ' + fsc + ';background:transparent;color:' + fsc + ';cursor:pointer;font-size:10px;line-height:1.4;flex-shrink:0">⛶</button>';

            gridHtml += '<div style="border:1px solid ' + cardBorder + ';border-radius:6px;padding:4px 6px;min-width:0">' +
                '<div style="display:flex;align-items:center;gap:4px;margin-bottom:2px">' +
                '<span style="font-size:11px;font-weight:700;color:' + fsc + ';flex:1">' + fsName + '</span>' +
                expandBtn +
                '</div>' +
                tbl + '</div>';
        }
        gridHtml += '</div>';
        daemonsEl.innerHTML = fsIds.length > 0 ? gridHtml : '';
        _refreshMdsFsModal();
    }

    // One row per filesystem, sorted by numeric fs_id (always show all, unfiltered)
    for (const fsId of allFsIds) {
        const fsName = fsNames[fsId] || ('FS #' + fsId);
        const active = activeByFs[fsId].sort((a, b) => a.rank - b.rank);

        const rankList = active.map(a => a.rank).join(', ');

        const siteCounts = {};
        for (const a of active) siteCounts[a.site] = (siteCounts[a.site] || 0) + 1;
        const siteBadges = Object.keys(siteCounts).sort().map(site => {
            const sc = siteColor(site);
            return '<span class="pg-state-badge mr-1" style="background:' + sc.bg + ';color:#fff">' + site + ' ' + siteCounts[site] + '</span>';
        }).join('');

        const totalSessions = active.reduce((sum, a) => sum + (daemonSessions[a.cephDaemon] || 0), 0);
        const sessionsCell = totalSessions > 0
            ? '<span class="text-sm font-semibold text-gray-900 dark:text-white">' + totalSessions.toLocaleString() + '</span>'
            : '<span class="text-gray-400">—</span>';

        const fsc = fsColor(fsId);
        tbody.insertAdjacentHTML('beforeend',
            '<tr class="hover:bg-gray-50 dark:hover:bg-gray-700">' +
            '<td class="px-3 py-2 text-sm font-semibold text-gray-900 dark:text-white" style="border-left:3px solid ' + fsc + '">' + fsName + '</td>' +
            '<td class="px-3 py-2 text-sm text-gray-500 dark:text-gray-400">' + fsId + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-semibold" style="color:' + fsc + '">' + active.length + '</td>' +
            '<td class="px-3 py-2 text-xs font-mono text-gray-500 dark:text-gray-400">' + rankList + '</td>' +
            '<td class="px-3 py-2">' + siteBadges + '</td>' +
            '<td class="px-3 py-2 text-right">' + sessionsCell + '</td>' +
            '</tr>'
        );
    }

    // Standby pool summary row
    if (standbyList.length > 0) {
        const siteCounts = {};
        for (const s of standbyList) siteCounts[s.site] = (siteCounts[s.site] || 0) + 1;
        const siteBadges = Object.keys(siteCounts).sort().map(site => {
            const sc = siteColor(site);
            return '<span class="pg-state-badge mr-1" style="background:' + sc.bg + '88;color:#fff">' + site + ' ' + siteCounts[site] + '</span>';
        }).join('');

        tbody.insertAdjacentHTML('beforeend',
            '<tr class="hover:bg-gray-50 dark:hover:bg-gray-700 border-t-2 border-dashed border-gray-300 dark:border-gray-600">' +
            '<td class="px-3 py-2 text-sm italic text-gray-400 dark:text-gray-500">standby pool</td>' +
            '<td class="px-3 py-2 text-sm text-gray-400">—</td>' +
            '<td class="px-3 py-2 text-sm text-right font-semibold text-blue-600 dark:text-blue-400">' + standbyList.length + '</td>' +
            '<td class="px-3 py-2 text-xs text-gray-400">—</td>' +
            '<td class="px-3 py-2">' + siteBadges + '</td>' +
            '<td class="px-3 py-2 text-right"><span class="text-gray-400">—</span></td>' +
            '</tr>'
        );
    }
}

`
