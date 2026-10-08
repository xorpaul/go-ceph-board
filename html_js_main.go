package main

const htmlJSMain = `
function updateOSDHosts(metrics, hostMetricsMap, daemonStartTimeMap) {
    daemonStartTimeMap = daemonStartTimeMap || {};
    // Build daemon -> host map from ceph_osd_metadata
    const metaArr = metrics['ceph_osd_metadata'] || [];
    const daemonHost = {}; // "osd.72" -> {hostname, site, ver}
    for (const m of metaArr) {
        const d = m.labels.ceph_daemon;
        if (!d) continue;
        const rawHn = m.labels.host || m.labels.hostname || d;
        // If Go resolved this short name to an FQDN (via IP-peer dedup or PTR lookup),
        // the alias is embedded in hostMetricsMap._canonical.  Use it so that rows
        // whose OSD daemons haven't been restarted since a hostname change show the
        // correct site and can join against the node_exporter metrics.
        const nmAlias = hostMetricsMap && hostMetricsMap[rawHn];
        const hostname = (nmAlias && nmAlias._canonical) || rawHn;
        const crushStr = [m.labels.datacenter, m.labels.room, m.labels.rack].filter(Boolean).join(' ');
        daemonHost[d] = { hostname, site: getSiteFromHostname(hostname, crushStr), ver: m.labels.ceph_version || '' };
    }
    const majVer = majorityVersion(metaArr);

    // Collect per-OSD data
    const upArr     = metrics['ceph_osd_up']               || [];
    const inArr     = metrics['ceph_osd_in']               || [];
    const bytesArr  = metrics['ceph_osd_stat_bytes']       || [];
    const usedArr   = metrics['ceph_osd_stat_bytes_used']  || [];
    const applyArr  = metrics['ceph_osd_apply_latency_ms'] || [];
    const commitArr = metrics['ceph_osd_commit_latency_ms']|| [];

    function toMap(arr) {
        const m = {};
        for (const e of arr) { if (e.labels.ceph_daemon) m[e.labels.ceph_daemon] = e.value; }
        return m;
    }
    const upMap     = toMap(upArr);
    const inMap     = toMap(inArr);
    const bytesMap  = toMap(bytesArr);
    const usedMap   = toMap(usedArr);
    const applyMap  = toMap(applyArr);
    const commitMap = toMap(commitArr);

    // Group by host
    const hosts = {}; // hostname -> {site, osds: [{daemon, up, in}], diskUsed, diskTotal, applies, commits, versions}
    for (const daemon of Object.keys(upMap)) {
        const meta = daemonHost[daemon];
        const hostname = meta ? meta.hostname : 'unknown';
        const site     = meta ? meta.site     : 'unknown';
        if (!hosts[hostname]) {
            hosts[hostname] = { site, osds: [], diskUsed: 0, diskTotal: 0, applies: [], commits: [], versions: new Set(), lastSvcStart: null, lastSvcDaemon: null };
        }
        const h = hosts[hostname];
        h.osds.push({ daemon, up: upMap[daemon] === 1, inn: inMap[daemon] === 1 });
        h.diskUsed  += usedMap[daemon]  || 0;
        h.diskTotal += bytesMap[daemon] || 0;
        if (applyMap[daemon]  !== undefined) h.applies.push(applyMap[daemon]);
        if (commitMap[daemon] !== undefined) h.commits.push(commitMap[daemon]);
        if (meta && meta.ver) h.versions.add(meta.ver);
        const st = daemonStartTimeMap[daemon];
        if (st !== undefined && (h.lastSvcStart === null || st > h.lastSvcStart)) {
            h.lastSvcStart  = st;
            h.lastSvcDaemon = daemon;
        }
    }

    // Sort: by site then hostname
    const sortedHosts = Object.entries(hosts).sort(([ha, a], [hb, b]) => {
        if (a.site !== b.site) return a.site.localeCompare(b.site);
        return ha.localeCompare(hb);
    });

    const tbody = document.getElementById('osdHostTableBody');
    tbody.innerHTML = '';

    if (sortedHosts.length === 0) {
        tbody.innerHTML = '<tr><td colspan="15" class="px-3 py-6 text-center text-gray-500">No OSD data found</td></tr>';
        return;
    }

    let lastSite = null;
    for (const [hostname, h] of sortedHosts) {
        if (h.site !== lastSite) {
            lastSite = h.site;
            const sc = siteColor(h.site);
            tbody.insertAdjacentHTML('beforeend',
                '<tr><td colspan="15" class="px-3 py-1 text-xs font-bold uppercase tracking-wider" ' +
                'style="background:' + sc.bg + ';color:#fff">' + h.site + ' — ' +
                sortedHosts.filter(([,d]) => d.site === h.site).length + ' hosts</td></tr>'
            );
        }

        h.osds.sort((a, b) => {
            const numA = parseInt(a.daemon.replace('osd.', ''));
            const numB = parseInt(b.daemon.replace('osd.', ''));
            return numA - numB;
        });

        const upCount  = h.osds.filter(o => o.up).length;
        const inCount  = h.osds.filter(o => o.inn).length;
        const total    = h.osds.length;

        const diskPct = h.diskTotal > 0 ? (h.diskUsed / h.diskTotal * 100) : 0;
        const dc      = diskColor(diskPct);

        const avgApply  = h.applies.length  > 0 ? h.applies.reduce((a,b) => a+b, 0) / h.applies.length   : null;
        const avgCommit = h.commits.length  > 0 ? h.commits.reduce((a,b) => a+b, 0) / h.commits.length   : null;

        const applyStr  = avgApply  !== null ? avgApply.toFixed(1)  : '-';
        const commitStr = avgCommit !== null ? avgCommit.toFixed(1) : '-';
        const applyCol  = avgApply  !== null ? latencyColor(avgApply)  : '#6b7280';
        const commitCol = avgCommit !== null ? latencyColor(avgCommit) : '#6b7280';

        // OSD status badges
        let badges = '<div class="flex flex-wrap gap-0.5">';
        for (const osd of h.osds) {
            let badgeColor;
            if (osd.up && osd.inn)       badgeColor = '#22c55e';   // up+in = green
            else if (osd.up && !osd.inn) badgeColor = '#f97316';   // up+out = orange
            else if (!osd.up && !osd.inn)badgeColor = '#ef4444';   // down+out = red
            else                          badgeColor = '#eab308';   // down+in = yellow (unusual)
            const num = osd.daemon.replace('osd.', '');
            badges += '<span class="osd-badge" style="background:' + badgeColor + '" title="' + osd.daemon + ' ' +
                (osd.up ? 'up' : 'down') + '/' + (osd.inn ? 'in' : 'out') + '">' +
                '</span>';
        }
        badges += '</div>';

        const sc = siteColor(h.site);

        let countColor = '#22c55e';
        if (upCount < total) countColor = '#ef4444';
        else if (inCount < total) countColor = '#f97316';

        const nm = (hostMetricsMap && hostMetricsMap[hostname]) || {};
        const hostDown = !!nm.unreachable;
        const downCell = '<span class="pg-state-badge" style="background:#ef4444;color:#fff" title="Host unreachable — node_exporter did not respond">unreachable</span>';
        tbody.insertAdjacentHTML('beforeend',
            '<tr class="hover:bg-gray-50 dark:hover:bg-gray-700"' + (hostDown ? ' style="opacity:0.75"' : '') + '>' +
            '<td class="px-3 py-2 text-sm font-mono font-medium text-gray-900 dark:text-white" style="border-left:3px solid ' + (hostDown ? '#ef4444' : sc.color) + '">' + hostname + (hostDown ? ' <span style="font-size:0.65rem;background:#ef4444;color:#fff;border-radius:3px;padding:1px 4px;vertical-align:middle">DOWN</span>' : '') + hostLinkHTML(hostname) + '</td>' +
            '<td class="px-3 py-2"><span class="pg-state-badge" style="background:' + sc.bg + ';color:#fff">' + h.site + '</span></td>' +
            '<td class="px-3 py-2 text-sm font-mono" style="color:' + countColor + '">' +
                upCount + 'up/' + inCount + 'in/' + total + '</td>' +
            '<td class="px-3 py-2" style="min-width:110px">' +
                '<div class="flex items-center gap-2">' +
                '<div class="disk-bar-bg flex-1"><div class="disk-bar-fill" style="width:' + Math.min(diskPct,100).toFixed(1) + '%;background:' + dc + '"></div></div>' +
                '<span class="text-xs font-mono font-bold w-10 text-right" style="color:' + dc + '">' + diskPct.toFixed(1) + '%</span>' +
                '</div>' +
            '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + formatBytes(h.diskUsed) + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono text-gray-600 dark:text-gray-300">' + formatBytes(h.diskTotal) + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono font-bold" style="color:' + applyCol  + '">' + applyStr  + '</td>' +
            '<td class="px-3 py-2 text-sm text-right font-mono font-bold" style="color:' + commitCol + '">' + commitStr + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : pctBarCellHtml(nm.cpuPct !== undefined ? nm.cpuPct : null, cpuColor, (nm.cpuCount ? nm.cpuCount + ' cores' + (nm.cpuModel ? '\n' + nm.cpuModel : '') : null), (nm.cpuCount ? nm.cpuCount + ' cores' : null))) + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : (function() {
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
            })()) + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : formatUptimeHtml(nm.bootTime !== undefined ? nm.bootTime : null)) + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : formatUptimeHtml(h.lastSvcStart, h.lastSvcDaemon ? 'Most recently restarted: ' + h.lastSvcDaemon : null)) + '</td>' +
            '<td class="px-3 py-2">' + (hostDown ? downCell : formatOsHtml(nm.osName || null)) + '</td>' +
            '<td class="px-3 py-2">' + cephVersionCellHtml(h.versions.size > 0 ? [...h.versions][0] : null, majVer) + '</td>' +
            '<td class="px-3 py-2">' + badges + '</td>' +
            '</tr>'
        );
    }
}

// updateCephVersionBadge computes the cluster-wide Ceph version from parsed
// metrics and updates the badge in the page header. If daemons report mixed
// versions (rolling upgrade in progress), the badge shows "mixed (N versions)".
function updateCephVersionBadge(metrics) {
    const badge = document.getElementById('cephVersionBadge');
    if (!badge) return;
    const counts = {};
    for (const family of ['ceph_mon_metadata', 'ceph_mds_metadata', 'ceph_osd_metadata']) {
        for (const m of (metrics[family] || [])) {
            const v = m.labels.ceph_version;
            if (v) counts[v] = (counts[v] || 0) + 1;
        }
    }
    const unique = Object.keys(counts);
    if (unique.length === 0) { badge.classList.add('hidden'); return; }
    unique.sort((a, b) => counts[b] - counts[a]);
    const parsed = parseCephVersion(unique[0]);
    if (!parsed) { badge.classList.add('hidden'); return; }
    if (unique.length > 1) {
        const shorts = unique.map(v => { const p = parseCephVersion(v); return p ? p.short : v; });
        badge.textContent = 'mixed (' + unique.length + ' versions)';
        badge.title = shorts.join(', ');
        badge.style.background = 'rgba(245,158,11,0.3)';
        badge.style.color = '#d97706';
    } else {
        badge.textContent = 'v' + parsed.short + ' (' + parsed.codename + ')';
        badge.title = parsed.full;
        badge.style.background = '';
        badge.style.color = '';
    }
    badge.classList.remove('hidden');
}

// ─── Main fetch loop ──────────────────────────────────────────────────────────
const connectionStatusEl   = document.getElementById('connectionStatus');
const dashboardContentEl   = document.getElementById('dashboardContent');
const clusterSwitcher      = document.getElementById('clusterSwitcher');

let monitorInterval = null;
let chartsInitialized = false;
let currentCluster = '';
let fetchGeneration = 0;
let knownClusters = [];
let clusterVersions = {};                  // cluster name -> version string from /clusters
let clusterGraphConfigs = {};              // cluster name -> []PromGraphFrontendConfig from /clusters
let clusterProductionCephFSVolumes = {};   // cluster name -> [fsName, ...] (e.g. ["fs1","fs2",...])
let documentHeader = '';                   // global document_header text from config
let showAllMDS = false;                    // false = production-only filter, true = show all
let clusterHealthStatus = {};              // cluster name -> numeric health value (0/1/2)
let globalThresholds = null;               // warn/crit boundaries from /clusters, applied to all charts

function healthEmoji(val) {
    if (val === 0) return '✅';
    if (val === 1) return '⚠️';
    if (val >= 2)  return '🔴';
    return '';
}

function clusterBtnLabel(name) {
    const prefix = name.startsWith('EU') ? '🇪🇺 ' : name.startsWith('US') ? '🇺🇸 ' : '';
    const ver    = clusterVersions[name] || '';
    const emoji  = clusterHealthStatus[name] !== undefined ? ' ' + healthEmoji(clusterHealthStatus[name]) : '';
    return prefix + name + (ver ? ' — ' + ver : '') + emoji;
}

function applyClusterHealthLabel(name) {
    const btn = document.querySelector('.cluster-btn[data-cluster="' + CSS.escape(name) + '"]');
    if (btn) { btn.textContent = clusterBtnLabel(name); return; }
    const sel = document.getElementById('clusterSelect');
    if (sel) {
        for (const opt of sel.options) {
            if (opt.value === name) { opt.textContent = clusterBtnLabel(name); return; }
        }
    }
}

async function probeClusterHealth(name) {
    try {
        const r = await fetch('/ceph-metrics?cluster=' + encodeURIComponent(name));
        if (!r.ok) return;
        const text = await r.text();
        for (const line of text.split('\n')) {
            if (/^ceph_health_status[\s{]/.test(line) && !line.startsWith('#')) {
                const parts = line.trim().split(/\s+/);
                const val = parseFloat(parts[parts.length - 1]);
                if (!isNaN(val)) { clusterHealthStatus[name] = val; applyClusterHealthLabel(name); }
                break;
            }
        }
    } catch (_) { /* non-fatal */ }
}

// Extract cluster name from /cluster/<name> path, or null if not present.
function clusterFromPath() {
    const m = window.location.pathname.match(/^\/cluster\/(.+)$/);
    return m ? decodeURIComponent(m[1]) : null;
}

const CLUSTER_BTN_BASE    = 'cluster-btn';
const CLUSTER_BTN_ACTIVE  = 'cluster-btn active';
const CLUSTER_BTN_INACTIVE = 'cluster-btn';

// Switch to a named cluster, optionally pushing a new history entry.
function doClusterSwitch(name, pushHistory) {
    if (pushHistory !== false) {
        history.pushState(null, '', '/cluster/' + encodeURIComponent(name));
    }
    const sel = document.getElementById('clusterSelect');
    if (sel) sel.value = name;
    document.querySelectorAll('.cluster-btn').forEach(btn => {
        btn.className = btn.dataset.cluster === name ? CLUSTER_BTN_ACTIVE : CLUSTER_BTN_INACTIVE;
    });
    if (name === currentCluster) return;
    currentCluster = name;
    if (monitorInterval) { clearInterval(monitorInterval); monitorInterval = null; }
    resetChartsAndHistory();
    initDynGraphs(clusterGraphConfigs[name] || []);
    chartsInitialized = false;
    dashboardContentEl.classList.add('hidden');
    updateMDSFilterBar();
    startMonitoring();
}

function updateDocumentHeader() {
    const el = document.getElementById('docHeaderDetails');
    if (!el) return;
    if (!documentHeader) { el.classList.add('hidden'); return; }
    el.classList.remove('hidden');
    const content = document.getElementById('docHeaderContent');
    if (content) content.textContent = documentHeader;
}

function updateMDSFilterBar() {
    const bar  = document.getElementById('mdsFilterBar');
    const btn  = document.getElementById('mdsFilterBtn');
    const desc = document.getElementById('mdsFilterDesc');
    const text = document.getElementById('mdsFilterBtnText');
    if (!bar) return;
    const prod = productionMDSSet();
    if (prod.size === 0) {
        bar.classList.add('hidden');
        return;
    }
    bar.classList.remove('hidden');
    const list = [...prod].sort().join(', ');
    if (showAllMDS) {
        btn.style.background = '#6b7280';
        btn.style.borderColor = '#6b7280';
        btn.style.color = '#fff';
        text.textContent = 'Show All MDS';
        if (desc) desc.textContent = 'Displaying all filesystems. Production: ' + list;
    } else {
        btn.style.background = '#2563eb';
        btn.style.borderColor = '#2563eb';
        btn.style.color = '#fff';
        text.textContent = 'Production Only';
        if (desc) desc.textContent = 'Filtering to: ' + list + '. Click to show all.';
    }
}

function toggleMDSFilter() {
    showAllMDS = !showAllMDS;
    updateMDSFilterBar();
    fetchAllData();
}

async function initClusters() {
    try {
        const resp = await fetch('/clusters');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const data = await resp.json();
        knownClusters = data.clusters || [];
        clusterVersions = data.versions || {};
        clusterGraphConfigs = data.prom_graphs || {};
        clusterProductionCephFSVolumes = data.production_ceph_fs_volumes || {};
        documentHeader = data.document_header || '';
        updateDocumentHeader();
        globalThresholds = data.thresholds || null;
        configSites = data.sites || [];
        configHostLink = data.host_link || null;
        const defaultCluster = data.default || (knownClusters.length > 0 ? knownClusters[0] : '');

        // Honour /cluster/<name> deeplink; fall back to configured default.
        const urlCluster = clusterFromPath();
        currentCluster = (urlCluster && knownClusters.includes(urlCluster)) ? urlCluster : defaultCluster;
        updateMDSFilterBar(); // must be after currentCluster is set
        initDynGraphs(clusterGraphConfigs[currentCluster] || []);

        if (knownClusters.length > 1) {
            clusterSwitcher.innerHTML = '';
            if (knownClusters.length < 5) {
                // Render as clickable button pills
                for (const name of knownClusters) {
                    const btn = document.createElement('button');
                    btn.dataset.cluster = name;
                    btn.textContent = clusterBtnLabel(name);
                    btn.className = name === currentCluster ? CLUSTER_BTN_ACTIVE : CLUSTER_BTN_INACTIVE;
                    btn.addEventListener('click', () => doClusterSwitch(name));
                    clusterSwitcher.appendChild(btn);
                }
            } else {
                // Render as a labelled dropdown
                const label = document.createElement('label');
                label.htmlFor = 'clusterSelect';
                label.className = 'text-base font-semibold text-gray-700 dark:text-gray-200 whitespace-nowrap';
                label.textContent = 'Cluster';
                const sel = document.createElement('select');
                sel.id = 'clusterSelect';
                sel.className = 'text-base font-semibold rounded-lg border-2 border-blue-400 dark:border-blue-500 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-500 cursor-pointer shadow-sm';
                for (const name of knownClusters) {
                    const opt = document.createElement('option');
                    opt.value = name;
                    opt.textContent = clusterBtnLabel(name);
                    if (name === currentCluster) opt.selected = true;
                    sel.appendChild(opt);
                }
                sel.addEventListener('change', () => {
                    const name = sel.value;
                    if (name === currentCluster) return;
                    doClusterSwitch(name);
                });
                clusterSwitcher.appendChild(label);
                clusterSwitcher.appendChild(sel);
            }
            clusterSwitcher.classList.remove('hidden');
            // Probe health for all clusters concurrently; labels update as results arrive.
            for (const name of knownClusters) probeClusterHealth(name);
        }

        const links = data.external_links || [];
        if (links.length > 0) {
            const container = document.getElementById('externalLinks');
            if (container) {
                container.innerHTML = links.map(l =>
                    '<a href="' + l.url + '" target="_blank" rel="noopener noreferrer" ' +
                    'class="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg text-sm font-medium ' +
                    'bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-200 ' +
                    'hover:bg-gray-300 dark:hover:bg-gray-600 transition-colors">' +
                    '<svg xmlns="http://www.w3.org/2000/svg" class="w-3.5 h-3.5 opacity-60" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"/></svg>' +
                    l.label + '</a>'
                ).join('');
                container.classList.remove('hidden');
            }
        }
    } catch (err) {
        console.warn('Could not fetch /clusters, using default:', err);
    }
}

function setStatus(msg, color) {
    connectionStatusEl.innerHTML = msg;
    connectionStatusEl.style.color = color;
}

async function fetchAllData() {
    const myGeneration = ++fetchGeneration;
    const myCluster = currentCluster;
    try {
        const clusterParam = myCluster ? '?cluster=' + encodeURIComponent(myCluster) : '';
        const myGraphs = clusterGraphConfigs[myCluster] || [];
        const [cephResult, nodeResult, rankResult, ...graphResults] = await Promise.allSettled([
            fetch('/ceph-metrics' + clusterParam).then(async r => {
                if (!r.ok) {
                    const body = await r.text().catch(() => '');
                    throw new Error(body.trim() || ('HTTP ' + r.status + ' from /ceph-metrics'));
                }
                return r.text();
            }),
            fetch('/node-metrics' + clusterParam).then(r => { if (!r.ok) throw new Error('HTTP ' + r.status + ' from /node-metrics'); return r.text(); }),
            fetch('/rank-assignments' + clusterParam).then(r => { if (!r.ok) throw new Error('HTTP ' + r.status + ' from /rank-assignments'); return r.json(); }),
            ...myGraphs.map(g =>
                fetch('/prom-graph?id=' + encodeURIComponent(g.id) + (myCluster ? '&cluster=' + encodeURIComponent(myCluster) : ''))
                    .then(r => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
            ),
        ]);

        // Discard results from a superseded fetch (cluster was switched while in-flight)
        if (fetchGeneration !== myGeneration) return;

        if (cephResult.status === 'rejected') throw cephResult.reason;

        const metrics        = parsePrometheusText(cephResult.value);
        const nodeMetrics    = nodeResult.status === 'fulfilled' ? parsePrometheusText(nodeResult.value) : {};
        const hostMetricsMap = buildHostMetricsMap(nodeMetrics);
        const daemonMemMap       = buildDaemonMemMap(nodeMetrics);
        const daemonStartTimeMap = buildDaemonStartTimeMap(nodeMetrics);
        const daemonMemLimitMap  = buildDaemonMemLimitMap(nodeMetrics);
        const srLagMap           = buildSrLagMap(nodeMetrics);
        const journalLiveMap     = buildJournalLiveMap(nodeMetrics);
        const srPresentMap       = buildSrPresentMap(nodeMetrics);

        myGraphs.forEach((g, i) => {
            updateDynGraph(g.id, graphResults[i].status === 'fulfilled' ? graphResults[i].value : null, g);
        });

        dashboardContentEl.classList.remove('hidden');

        if (!chartsInitialized) {
            dbg('initCharts: before');
            debugPageHeight('pre-initCharts');
            initCharts();
            chartsInitialized = true;
            dbg('initCharts: after');
            debugChartSizes('post-initCharts');
            debugPageHeight('post-initCharts');
        }

        const metricKeys = Object.keys(metrics);
        dbg('fetch tick: ' + metricKeys.length + ' metric families: ' + metricKeys.slice(0,10).join(', ') + (metricKeys.length > 10 ? '...' : ''));
        dbg('health_status=' + (metrics['ceph_health_status'] ? metrics['ceph_health_status'][0].value : 'MISSING'));
        dbg('osd_up count=' + (metrics['ceph_osd_up'] ? metrics['ceph_osd_up'].length : 'MISSING'));
        dbg('node hosts in map: ' + Object.keys(hostMetricsMap).length);
        debugPageHeight('pre-update');

        // Keep the cluster switcher label in sync with the current health.
        if (myCluster && metrics['ceph_health_status']) {
            const hv = metrics['ceph_health_status'][0].value;
            if (clusterHealthStatus[myCluster] !== hv) {
                clusterHealthStatus[myCluster] = hv;
                applyClusterHealthLabel(myCluster);
            }
        }

        updateHealth(metrics);
        updateHealthDetail(metrics);
        updateCluster(metrics);
        updateOSDs(metrics);
        updatePGs(metrics);
        updateMonitors(metrics, hostMetricsMap);
        const rankAssignments = rankResult.status === 'fulfilled' ? rankResult.value : {};
        const daemonRankStateMap = buildDaemonRankStateMap(nodeMetrics);
        updateMDS(metrics, daemonStartTimeMap, rankAssignments, daemonMemLimitMap, srLagMap, journalLiveMap, srPresentMap, daemonRankStateMap);
        updateMDSDaemons(metrics, daemonRankStateMap);
        updateMdsTrimChart(metrics);
        drawMDSSankey(metrics);
        updatePools(metrics);
        updateMDSHostsTop(metrics, hostMetricsMap, daemonStartTimeMap, daemonRankStateMap);
        updateMDSMemCharts(metrics, hostMetricsMap, daemonMemMap, daemonRankStateMap);
        updateOSDHosts(metrics, hostMetricsMap, daemonStartTimeMap);
        updateCephVersionBadge(metrics);

        debugChartSizes('post-update');
        debugPageHeight('post-update');
        setStatus('Connected — last updated: ' + new Date().toLocaleTimeString(), '#22c55e');

    } catch (err) {
        // Use cluster check (not generation) so the error still shows even when
        // newer fetches for the same cluster are in-flight (e.g. 30s mgr timeout
        // while the 10s poll interval has already fired several times).
        if (currentCluster !== myCluster) return;
        console.error('Fetch error:', err);
        const clusterLabel = myCluster ? ' [' + myCluster + ']' : '';
        setStatus('<strong>No monitoring server reachable' + clusterLabel + ':</strong> ' + err.message, '#ef4444');
        dashboardContentEl.classList.add('hidden');
    }
}

function startMonitoring() {
    if (monitorInterval) clearInterval(monitorInterval);
    fetchAllData();
    monitorInterval = setInterval(fetchAllData, 10000);
}

// Auto-start on load: fetch cluster list first, then begin monitoring
setTimeout(() => initClusters().then(() => startMonitoring()), 300);

// Browser back/forward: switch cluster to match the restored URL.
window.addEventListener('popstate', () => {
    const name = clusterFromPath() || (knownClusters.length > 0 ? knownClusters[0] : '');
    if (name && name !== currentCluster) doClusterSwitch(name, false);
});

// ─── Column tooltips ──────────────────────────────────────────────────────────
(function() {
    const tip = document.createElement('div');
    tip.id = 'col-tooltip';
    document.body.appendChild(tip);
    function positionTip(e) {
        const margin = 14;
        const tw = tip.offsetWidth, th = tip.offsetHeight;
        let x = e.clientX + margin, y = e.clientY + margin;
        if (x + tw > window.innerWidth  - margin) x = e.clientX - tw - margin;
        if (y + th > window.innerHeight - margin) y = e.clientY - th - margin;
        tip.style.left = x + 'px';
        tip.style.top  = y + 'px';
    }
    document.querySelectorAll('.th-info').forEach(function(el) {
        el.addEventListener('mouseenter', function(e) {
            tip.textContent = el.getAttribute('data-tip');
            tip.style.display = 'block';
            positionTip(e);
        });
        el.addEventListener('mousemove', positionTip);
        el.addEventListener('mouseleave', function() { tip.style.display = 'none'; });
    });
})();
</script>
</body>
</html>
`
