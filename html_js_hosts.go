package main

const htmlJSHosts = `
function updateMDSHostsTop(metrics, hostMetricsMap, daemonStartTimeMap, daemonRankStateMap) {
    daemonStartTimeMap  = daemonStartTimeMap  || {};
    daemonRankStateMap  = daemonRankStateMap  || {};
    const tbody = document.getElementById('mdsHostsTopTableBody');
    if (!tbody) return;

    const metaArr = metrics['ceph_mds_metadata'] || [];
    if (metaArr.length === 0) {
        tbody.innerHTML = '<tr><td colspan="10" class="px-3 py-4 text-center text-gray-500 dark:text-gray-400">No MDS data found</td></tr>';
        return;
    }

    const mdsfsNames = {};
    for (const fs of (metrics['ceph_fs_metadata'] || [])) mdsfsNames[fs.labels.fs_id] = fs.labels.name;

    const mdsDaemonSessions = {};
    for (const m of (metrics['ceph_mds_sessions_session_count'] || [])) mdsDaemonSessions[m.labels.ceph_daemon] = m.value;

    // daemonRankStateMap is pre-built from ceph_mds_rank_assigned in node-metrics
    // (populated via extra_hosts scrape of misc/mon hosts running ceph_mdsmap_textfile.sh).
    // Falls back to ceph_mds_metadata.state when the textfile is not deployed.
    const hasRankAssigned = Object.keys(daemonRankStateMap).length > 0;

    const majVer = majorityVersion(metaArr);
    const topCanonMap = buildHostCanonMap(metaArr);
    const mdsPerHost = {};
    for (const m of metaArr) {
        const rawHn  = m.labels.hostname || m.labels.ceph_daemon;
        // Prefer IP-peer dedup (topCanonMap), then fall back to the PTR-resolved alias
        // embedded in hostMetricsMap._canonical.  This handles the case where old and
        // new daemons have different public_addr IPs (e.g. different ports or 0.0.0.0
        // on standby), preventing them from appearing as duplicate host rows.
        const nmAlias = hostMetricsMap && hostMetricsMap[rawHn];
        const hn     = topCanonMap[rawHn] || (nmAlias && nmAlias._canonical) || rawHn;
        const rank   = parseInt(m.labels.rank, 10);
        const fsId   = m.labels.fs_id;
        const fsName = mdsfsNames[fsId] || ('fs#' + fsId);
        if (!mdsPerHost[hn]) mdsPerHost[hn] = { byFs: {}, standbyReplay: 0, inactive: 0, versions: new Set(), lastSvcStart: null, lastSvcDaemon: null };
        if (m.labels.ceph_version) mdsPerHost[hn].versions.add(m.labels.ceph_version);
        const st = daemonStartTimeMap[m.labels.ceph_daemon];
        if (st !== undefined && (mdsPerHost[hn].lastSvcStart === null || st > mdsPerHost[hn].lastSvcStart)) {
            mdsPerHost[hn].lastSvcStart  = st;
            mdsPerHost[hn].lastSvcDaemon = m.labels.ceph_daemon;
        }
        const effState = hasRankAssigned
            ? (daemonRankStateMap[m.labels.ceph_daemon] || '')
            : (m.labels.state || '');
        if (effState.includes('standby-replay')) {
            mdsPerHost[hn].standbyReplay++;
        } else if (rank >= 0) {
            if (!mdsPerHost[hn].byFs[fsId]) mdsPerHost[hn].byFs[fsId] = { name: fsName, count: 0, sessions: 0 };
            mdsPerHost[hn].byFs[fsId].count++;
            mdsPerHost[hn].byFs[fsId].sessions += (mdsDaemonSessions[m.labels.ceph_daemon] || 0);
        } else {
            mdsPerHost[hn].inactive++;
        }
    }
    // Add any hosts explicitly declared in config that are absent from ceph_mds_metadata
    // (e.g. host is down and all its MDS daemons have failed over).
    if (hostMetricsMap) {
        for (const [hn, nm] of Object.entries(hostMetricsMap)) {
            if (!nm.configuredMDS) continue;
            // Resolve to canonical so we don't create a phantom short-name row when
            // the canonical entry already exists (e.g. "mds1" alias for "mds1.site2").
            const canonHn = (nm._canonical) || hn;
            if (!mdsPerHost[canonHn]) {
                mdsPerHost[canonHn] = { byFs: {}, standbyReplay: 0, inactive: 0, versions: new Set() };
            }
        }
    }

    const maxByFs = {};
    for (const md of Object.values(mdsPerHost)) {
        for (const [fsId, fd] of Object.entries(md.byFs)) {
            if (!maxByFs[fsId] || fd.count > maxByFs[fsId]) maxByFs[fsId] = fd.count;
        }
    }

    const topHnSiteMap = buildHostSiteMap(metaArr);
    const sortedHosts = Object.keys(mdsPerHost).sort((a, b) => {
        const sa = topHnSiteMap[a] || getSiteFromHostname(a, '');
        const sb = topHnSiteMap[b] || getSiteFromHostname(b, '');
        return sa !== sb ? sa.localeCompare(sb) : a.localeCompare(b);
    });

    tbody.innerHTML = '';
    let lastSite = null;
    for (const hostname of sortedHosts) {
        const site = topHnSiteMap[hostname] || getSiteFromHostname(hostname, '');
        const sc   = siteColor(site);
        const nm   = (hostMetricsMap && hostMetricsMap[hostname]) || {};
        const md   = mdsPerHost[hostname];

        if (site !== lastSite) {
            lastSite = site;
            const siteCount = sortedHosts.filter(h => (topHnSiteMap[h] || getSiteFromHostname(h, '')) === site).length;
            tbody.insertAdjacentHTML('beforeend',
                '<tr><td colspan="10" class="px-3 py-1 text-xs font-bold uppercase tracking-wider" ' +
                'style="background:' + sc.bg + ';color:#fff">' + site + ' — ' + siteCount + ' MDS hosts</td></tr>'
            );
        }

        const totalSessions = Object.values(md.byFs).reduce((s, fd) => s + fd.sessions, 0);
        const totalActive   = Object.values(md.byFs).reduce((s, fd) => s + fd.count, 0);

        const maxActive = Math.max(...Object.keys(mdsPerHost).map(h => Object.values(mdsPerHost[h].byFs).reduce((s,fd)=>s+fd.count,0)), 1);
        const activeOpacity = (0.5 + 0.5 * (totalActive / maxActive)).toFixed(2);

        const fsEntries = Object.entries(md.byFs).sort(([a],[b]) => parseInt(a)-parseInt(b));
        let mdsCell = '<div>';
        mdsCell += '<div class="flex items-center gap-1 mb-0.5">';
        mdsCell += '<span class="pg-state-badge" style="background:rgba(34,197,94,' + activeOpacity + ');color:#fff" title="' + totalActive + ' active rank(s)">' + totalActive + ' active</span>';
        if (md.standbyReplay > 0) mdsCell += '<span class="pg-state-badge" style="background:#6366f1;color:#fff" title="' + md.standbyReplay + ' standby-replay daemon(s) — hot spare, replaying rank journal">' + md.standbyReplay + ' replay</span>';
        if (md.inactive > 0) mdsCell += '<span class="pg-state-badge" style="background:#6b7280;color:#fff" title="' + md.inactive + ' inactive/standby-pool daemon(s) — moveable, no assigned rank">' + md.inactive + ' inactive</span>';
        mdsCell += '</div>';
        mdsCell += '<div class="flex flex-wrap items-center gap-0.5">';
        for (const [fsId, fd] of fsEntries) {
            const c  = fsColor(fsId);
            const t  = maxByFs[fsId] > 0 ? fd.count / maxByFs[fsId] : 1;
            const fs = (0.55 + 0.30 * t).toFixed(2) + 'rem';
            const pv = t >= 0.7 ? 2 : 1;
            const ph = Math.round(3 + 2 * t);
            const sessStr = fd.sessions > 0 ? ' · ' + fd.sessions.toLocaleString() + ' sess' : '';
            mdsCell += '<span style="background:' + c + ';color:#fff;font-size:' + fs + ';padding:' + pv + 'px ' + ph + 'px;border-radius:3px;white-space:nowrap;display:inline-block;line-height:1.4" title="' + fd.count + ' active rank(s) on ' + fd.name + ', ' + fd.sessions.toLocaleString() + ' client sessions (max ranks: ' + maxByFs[fsId] + ')">' + fd.name + ' \xd7' + fd.count + sessStr + '</span>';
        }
        mdsCell += '</div></div>';

        const hostDown = !!nm.unreachable;
        const downCell = '<span class="pg-state-badge" style="background:#ef4444;color:#fff" title="Host unreachable — node_exporter did not respond">unreachable</span>';
        tbody.insertAdjacentHTML('beforeend',
            '<tr class="hover:bg-gray-50 dark:hover:bg-gray-700"' + (hostDown ? ' style="opacity:0.75"' : '') + '>' +
            '<td class="px-3 py-2 text-sm font-mono font-medium text-gray-900 dark:text-white" style="border-left:3px solid ' + (hostDown ? '#ef4444' : sc.color) + '">' + hostname + (hostDown ? ' <span style="font-size:0.65rem;background:#ef4444;color:#fff;border-radius:3px;padding:1px 4px;vertical-align:middle">DOWN</span>' : '') + hostLinkHTML(hostname) + '</td>' +
            '<td class="px-3 py-2"><span class="pg-state-badge" style="background:' + sc.bg + ';color:#fff">' + site + '</span></td>' +
            '<td class="px-3 py-2">' + mdsCell + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : pctBarCellHtml(nm.cpuPct !== undefined ? nm.cpuPct : null, cpuColor, (nm.cpuCount ? nm.cpuCount + ' cores' + (nm.cpuModel ? '\n' + nm.cpuModel : '') : null), (nm.cpuCount ? nm.cpuCount + ' cores' : null))) + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : (function() {
                const pct = nm.memPct !== undefined ? nm.memPct : null;
                if (pct === null) return '<span class="text-xs text-gray-400">—</span>';
                const color = diskColor(pct);
                const used  = nm.memTotal && nm.memAvail ? nm.memTotal - nm.memAvail : null;
                return '<div class="flex items-center gap-1">' +
                    '<div class="disk-bar-bg" style="width:48px"><div class="disk-bar-fill" style="width:' + Math.min(pct,100).toFixed(0) + '%;background:' + color + '"></div></div>' +
                    '<div><span class="text-xs font-mono font-bold" style="color:' + color + '">' + pct.toFixed(0) + '%</span>' +
                    (used !== null ? '<br><span style="font-size:0.6rem" class="text-gray-400 dark:text-gray-500 font-mono">' + formatBytes(used) + ' used</span>' : '') +
                    (nm.memAvail ? '<br><span style="font-size:0.6rem" class="text-gray-400 dark:text-gray-500 font-mono">' + formatBytes(nm.memAvail) + ' free</span>' : '') +
                    (nm.memTotal ? '<br><span style="font-size:0.6rem" class="text-gray-400 dark:text-gray-500 font-mono">' + formatBytes(nm.memTotal) + ' total</span>' : '') +
                    '</div></div>';
            })()) + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : formatUptimeHtml(nm.bootTime !== undefined ? nm.bootTime : null)) + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : formatUptimeHtml(md.lastSvcStart, md.lastSvcDaemon ? 'Most recently restarted: ' + md.lastSvcDaemon : null)) + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : formatOsHtml(nm.osName || null)) + '</td>' +
            '<td class="px-3 py-2">' + cephVersionCellHtml(md.versions.size > 0 ? [...md.versions][0] : null, majVer) + '</td>' +
            '<td class="px-3 py-2 text-right">' + (totalSessions > 0 ? '<span class="text-sm font-semibold text-gray-900 dark:text-white">' + totalSessions.toLocaleString() + '</span>' : '<span class="text-gray-400">—</span>') + '</td>' +
            '</tr>'
        );
    }
}

// ─── MDS Host Memory Breakdown Charts ────────────────────────────────────────
const mdsMemCharts = {}; // hostname -> Chart instance

function updateMDSMemCharts(metrics, hostMetricsMap, daemonMemMap, daemonRankStateMap) {
    daemonMemMap       = daemonMemMap       || {};
    daemonRankStateMap = daemonRankStateMap || {};
    const grid = document.getElementById('mdsMemChartsGrid');
    if (!grid) return;

    const metaArr = metrics['ceph_mds_metadata'] || [];
    if (metaArr.length === 0) {
        grid.innerHTML = '<p class="col-span-3 text-center text-gray-400 py-4">No MDS data found</p>';
        return;
    }

    // Build per-daemon info: hostname, rank, fsId, state
    const daemonMeta = {};
    for (const m of metaArr) {
        daemonMeta[m.labels.ceph_daemon] = {
            hostname: m.labels.hostname || '',
            rank:     parseInt(m.labels.rank, 10),
            fsId:     m.labels.fs_id || '0',
            state:    m.labels.state || '',
        };
    }

    const memCanonMap = buildHostCanonMap(metaArr);

    // RSS is in KB; convert to bytes for consistent comparisons with node_exporter
    const daemonRss = {};
    for (const m of (metrics['ceph_mds_mem_rss'] || [])) {
        daemonRss[m.labels.ceph_daemon] = m.value * 1024;
    }

    // Classification priority:
    // 1. daemonRankStateMap (from ceph_mds_rank_assigned in node-metrics, via extra_hosts) — most reliable
    // 2. ceph_mds_mem_cap — CAPS > 0 means active (serving clients)
    // 3. state label from ceph_mds_metadata — last resort, often empty/unreliable
    const hasMemRankAssigned = Object.keys(daemonRankStateMap).length > 0;

    const daemonCaps = {};
    for (const m of (metrics['ceph_mds_mem_cap'] || [])) {
        daemonCaps[m.labels.ceph_daemon] = m.value;
    }
    const capsDataPresent = Object.keys(daemonCaps).length > 0;

    function classifyDaemon(daemon, meta) {
        if (meta.rank < 0) return 'standby';
        if (hasMemRankAssigned) {
            const st = daemonRankStateMap[daemon] || '';
            return st.includes('standby-replay') ? 'standbyReplay' : 'active';
        }
        if (capsDataPresent) {
            return (daemonCaps[daemon] || 0) > 0 ? 'active' : 'standbyReplay';
        }
        return meta.state.includes('standby-replay') ? 'standbyReplay' : 'active';
    }

    // Group per host: active, standby-replay, and standby (pure passive).
    // rss is kept as undefined when not in ceph_mds_mem_rss (daemon not started /
    // pure standby with no process); this lets the legend show '—' rather than '0 B'.
    const hostData = {}; // hostname -> { active: [{daemon, fsId, rss}], standbyReplay: [...], standby: [{daemon, rss}] }
    for (const [daemon, meta] of Object.entries(daemonMeta)) {
        if (!meta.hostname) continue;
        const nmAlias = hostMetricsMap && hostMetricsMap[meta.hostname];
        const hn = memCanonMap[meta.hostname] || (nmAlias && nmAlias._canonical) || meta.hostname;
        if (!hostData[hn]) hostData[hn] = { active: [], standbyReplay: [], standby: [] };
        // prefer ceph_mds_mem_rss (KB→bytes); fall back to ceph_daemon_memory_bytes from textfile
        const rss = daemonRss[daemon] !== undefined ? daemonRss[daemon] : daemonMemMap[daemon];
        const entry = { daemon, fsId: meta.fsId, rss, fromTextfile: daemonRss[daemon] === undefined && daemonMemMap[daemon] !== undefined };
        hostData[hn][classifyDaemon(daemon, meta)].push(entry);
    }

    const memHnSiteMap = buildHostSiteMap(metaArr);
    const sortedHosts = Object.keys(hostData).sort((a, b) => {
        const sa = memHnSiteMap[a] || getSiteFromHostname(a, '');
        const sb = memHnSiteMap[b] || getSiteFromHostname(b, '');
        return sa !== sb ? sa.localeCompare(sb) : a.localeCompare(b);
    });

    // Remove stale charts for hosts that disappeared
    for (const hn of Object.keys(mdsMemCharts)) {
        if (!hostData[hn]) { mdsMemCharts[hn].destroy(); delete mdsMemCharts[hn]; }
    }
    // Clear grid on first run (replaces placeholder text)
    if (Object.keys(mdsMemCharts).length === 0) grid.innerHTML = '';

    // Warn when ceph_mds_mem_rss is absent entirely but active daemons exist.
    // This happens when the mgr prometheus module runs with exclude_perf_counters=true.
    const activeCount = Object.values(hostData).reduce((n, hd) => n + hd.active.length, 0);
    const rssAbsent = activeCount > 0 && Object.keys(daemonRss).length === 0;
    const existingBanner = document.getElementById('mdsMemRssBanner');
    if (rssAbsent && !existingBanner) {
        const banner = document.createElement('p');
        banner.id = 'mdsMemRssBanner';
        banner.className = 'col-span-3 text-xs text-amber-700 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-700 rounded px-3 py-2 mb-2';
        banner.innerHTML = '<strong>ceph_mds_mem_rss not available</strong> — the Ceph mgr Prometheus module is running with <code>mgr/prometheus/exclude_perf_counters = true</code>, which suppresses per-daemon memory metrics. Enable perf counters to see RSS here.';
        grid.prepend(banner);
    } else if (!rssAbsent && existingBanner) {
        existingBanner.remove();
    }

    for (const hostname of sortedHosts) {
        const hd = hostData[hostname];
        const nm = (hostMetricsMap && hostMetricsMap[hostname]) || {};
        const memTotal = nm.memTotal || 0;
        const memAvail = nm.memAvail || 0;

        const rssOf = e => e.rss !== undefined ? e.rss : 0;

        // Sort by reported RSS desc; daemons with no RSS data sort to the end
        hd.active.sort((a, b) => rssOf(b) - rssOf(a));
        hd.standbyReplay.sort((a, b) => rssOf(b) - rssOf(a));
        hd.standby.sort((a, b) => rssOf(b) - rssOf(a));

        const activeTotalRss        = hd.active.reduce((s, e) => s + rssOf(e), 0);
        const standbyReplayTotalRss = hd.standbyReplay.reduce((s, e) => s + rssOf(e), 0);
        const mdsTotalRss           = activeTotalRss + standbyReplayTotalRss;
        const otherRss         = Math.max(0, memTotal - mdsTotalRss - memAvail);

        const mdsPct  = memTotal > 0 ? mdsTotalRss / memTotal * 100 : 0;
        const freePct = memTotal > 0 ? memAvail / memTotal * 100 : 0;

        // Three aggregate slices: Active MDS, Standby-Replay MDS, Other, Free
        const labels = [], data = [], colors = [];
        if (activeTotalRss > 0)        { labels.push('Active MDS');        data.push(activeTotalRss);        colors.push('#60a5fa'); }
        if (standbyReplayTotalRss > 0) { labels.push('Standby-Replay MDS'); data.push(standbyReplayTotalRss); colors.push('#a78bfa'); }
        if (otherRss > 0)              { labels.push('Other (OS/OSDs)');   data.push(otherRss);              colors.push('#374151'); }
        labels.push('Free'); data.push(memAvail); colors.push('#34d399');

        if (mdsMemCharts[hostname]) {
            const c = mdsMemCharts[hostname];
            c.data.labels = labels;
            c.data.datasets[0].data = data;
            c.data.datasets[0].backgroundColor = colors;
            c.update('none');
        } else {
            const shortHost = hostname.split('.')[0];
            const site = memHnSiteMap[hostname] || getSiteFromHostname(hostname, '');
            const sc   = siteColor(site);
            const memPctStr = memTotal > 0
                ? '<span class="font-bold" style="color:' + diskColor(mdsPct) + '">' + mdsPct.toFixed(0) + '%</span> MDS · ' +
                  '<span class="font-bold" style="color:#34d399">' + freePct.toFixed(0) + '%</span> free'
                : '<span class="text-gray-400">no host data</span>';

            const card = document.createElement('div');
            card.id = 'mdsMemCard-' + hostname.replace(/\./g, '-');
            card.className = 'border border-gray-200 dark:border-gray-700 rounded-lg p-3';
            card.innerHTML =
                '<div class="flex items-center gap-2 mb-1">' +
                '<span class="text-sm font-semibold text-gray-800 dark:text-white">' + shortHost + '</span>' +
                '<span class="pg-state-badge" style="background:' + sc.bg + ';color:#fff">' + site + '</span>' +
                '</div>' +
                '<p id="mdsMemSummary-' + hostname.replace(/\./g, '-') + '" class="text-xs text-gray-500 dark:text-gray-400 mb-2">' + memPctStr + '</p>' +
                '<div style="height:180px;position:relative">' +
                '<canvas id="mdsMemCanvas-' + hostname.replace(/\./g, '-') + '"></canvas>' +
                '</div>' +
                '<div id="mdsMemLegend-' + hostname.replace(/\./g, '-') + '" class="mt-2"></div>';
            grid.appendChild(card);

            const canvas = document.getElementById('mdsMemCanvas-' + hostname.replace(/\./g, '-'));
            const chart = new Chart(canvas, {
                type: 'doughnut',
                data: { labels, datasets: [{ data, backgroundColor: colors, borderWidth: 1, borderColor: 'rgba(0,0,0,0.1)' }] },
                options: {
                    animation: false,
                    responsive: true,
                    maintainAspectRatio: false,
                    cutout: '60%',
                    plugins: {
                        legend: { display: false },
                        tooltip: {
                            callbacks: {
                                label: ctx => ' ' + ctx.label + ': ' + formatBytes(ctx.raw)
                            }
                        }
                    }
                }
            });
            mdsMemCharts[hostname] = chart;
        }

        // Update summary line and legend
        const summaryEl = document.getElementById('mdsMemSummary-' + hostname.replace(/\./g, '-'));
        if (summaryEl) {
            summaryEl.innerHTML = memTotal > 0
                ? '<span class="font-bold" style="color:' + diskColor(mdsPct) + '">' + mdsPct.toFixed(0) + '%</span> MDS (' + formatBytes(mdsTotalRss) + ') · ' +
                  '<span class="font-bold" style="color:#34d399">' + freePct.toFixed(0) + '%</span> free (' + formatBytes(memAvail) + ')'
                : '<span class="text-gray-400">no node_exporter data</span>';
        }

        const legendEl = document.getElementById('mdsMemLegend-' + hostname.replace(/\./g, '-'));
        if (legendEl) {
            let html = '<div class="text-xs space-y-1">';
            if (hd.active.length > 0) {
                const sumStr = activeTotalRss > 0 ? ' — ' + formatBytes(activeTotalRss) : '';
                html += '<div class="flex items-center gap-1 font-semibold text-gray-600 dark:text-gray-300">' +
                    '<span style="background:#60a5fa;display:inline-block;width:8px;height:8px;border-radius:2px;flex-shrink:0"></span>' +
                    'Active (' + hd.active.length + ')' + sumStr + '</div>';
            }
            if (hd.standbyReplay.length > 0) {
                const sumStr = standbyReplayTotalRss > 0 ? ' — ' + formatBytes(standbyReplayTotalRss) : '';
                html += '<div class="flex items-center gap-1 font-semibold text-gray-600 dark:text-gray-300">' +
                    '<span style="background:#a78bfa;display:inline-block;width:8px;height:8px;border-radius:2px;flex-shrink:0"></span>' +
                    'Standby Replay (' + hd.standbyReplay.length + ')' + sumStr + '</div>';
            }
            if (hd.standby.length > 0) {
                html += '<div class="text-gray-400 dark:text-gray-500 italic">Inactive / Standby Pool (' + hd.standby.length + ')</div>';
            }
            html += '</div>';
            legendEl.innerHTML = html;
        }
    }
}

`
