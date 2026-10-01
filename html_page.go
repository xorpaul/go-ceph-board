package main

const htmlPage = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>go-ceph-board</title>
    <link rel="icon" type="image/x-icon" href="/favicon.ico">
    <link rel="stylesheet" href="/static/css/tailwind.css">
    <script src="/static/js/chart.min.js"></script>
    <script src="/static/js/chartjs-adapter-date-fns.bundle.min.js"></script>
    <style>
        body {
            font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
            transition: background-color 0.3s ease, color 0.3s ease;
        }
        .metric-card { transition: all 0.3s ease-in-out; }
        .metric-card:hover { transform: translateY(-3px); box-shadow: 0 10px 15px -3px rgba(0,0,0,0.15); }
        .status-dot { height: 1rem; width: 1rem; border-radius: 50%; display: inline-block; animation: pulse 2s infinite; }
        @keyframes pulse { 0%,100% { opacity: 1; } 50% { opacity: 0.5; } }

        .dark { background-color: rgb(17,24,39); color: rgb(243,244,246); }
        .dark .bg-white { background-color: rgb(31,41,55) !important; }
        .dark .bg-gray-50 { background-color: rgb(55,65,81) !important; }
        .dark .bg-gray-100 { background-color: rgb(17,24,39) !important; }
        .dark .text-gray-900 { color: rgb(243,244,246) !important; }
        .dark .text-gray-500 { color: rgb(156,163,175) !important; }
        .dark .text-gray-600 { color: rgb(209,213,219) !important; }
        .dark .text-gray-700 { color: rgb(229,231,235) !important; }
        .dark .border-gray-200 { border-color: rgb(55,65,81) !important; }
        .dark .divide-gray-200 > :not([hidden]) ~ :not([hidden]) { border-color: rgb(55,65,81) !important; }
        .dark .shadow-md { box-shadow: 0 4px 6px -1px rgba(0,0,0,0.3); }

        .osd-badge {
            display: inline-block; width: 14px; height: 14px; border-radius: 2px;
            margin: 1px; cursor: default; flex-shrink: 0;
        }
        .disk-bar-bg { height: 6px; border-radius: 3px; background: #d1d5db; overflow: hidden; }
        .dark .disk-bar-bg { background: #374151; }
        .disk-bar-fill { height: 100%; border-radius: 3px; transition: width 0.5s ease; }

        .pg-state-badge {
            display: inline-block; padding: 1px 5px; border-radius: 3px;
            font-size: 10px; font-weight: bold; font-family: monospace;
        }
        .th-info {
            display: inline-flex; align-items: center; justify-content: center;
            width: 13px; height: 13px; border-radius: 50%;
            background: #6b7280; color: white;
            font-size: 8px; font-weight: bold; font-style: normal;
            cursor: help; margin-left: 4px; flex-shrink: 0; vertical-align: middle;
        }
        #col-tooltip {
            display: none; position: fixed; z-index: 9999;
            max-width: 280px; padding: 8px 12px; border-radius: 8px;
            background: rgba(17,24,39,0.97); color: #f3f4f6;
            font-size: 11px; font-weight: normal; text-transform: none;
            line-height: 1.5; pointer-events: none;
            box-shadow: 0 4px 12px rgba(0,0,0,0.5);
            border: 1px solid rgba(255,255,255,0.1);
        }
        html { scroll-behavior: smooth; }
        .nav-pill {
            display:inline-flex; align-items:center;
            padding:3px 10px; border-radius:12px;
            font-size:12px; font-weight:600;
            background:rgba(107,114,128,0.15); color:#374151;
            text-decoration:none; transition:background 0.15s;
        }
        .nav-pill:hover { background:rgba(107,114,128,0.3); }
        .dark .nav-pill { color:#d1d5db; }
        .mds-site-primary { font-weight:700; }

        .cluster-btn {
            font-size: 0.875rem; font-weight: 600; border-radius: 0.5rem;
            border: 2px solid transparent; padding: 0.375rem 0.75rem;
            cursor: pointer; transition: background-color 0.15s, border-color 0.15s, box-shadow 0.15s;
            box-shadow: 0 1px 2px rgba(0,0,0,0.05);
            background: #e5e7eb; color: #374151;
        }
        .cluster-btn:hover { background: #d1d5db; }
        .cluster-btn.active {
            background: #2563eb; color: #fff;
            border-color: #22c55e;
            box-shadow: 0 0 0 3px rgba(34,197,94,0.35);
        }
        .dark .cluster-btn { background: #374151; color: #e5e7eb; }
        .dark .cluster-btn:hover { background: #4b5563; }
        .dark .cluster-btn.active {
            background: #3b82f6; color: #fff;
            border-color: #4ade80;
            box-shadow: 0 0 0 3px rgba(74,222,128,0.3);
        }
    </style>
</head>
<body class="bg-gray-100 text-gray-800 transition-colors duration-300">

<div class="max-w-none mx-auto p-2 md:p-4">
    <header class="mb-4 flex justify-between items-center flex-wrap gap-2">
        <div>
            <h1 class="text-4xl font-bold text-gray-900 dark:text-white">go-ceph-board<span class="ml-2 align-middle text-sm font-mono font-normal text-gray-400 dark:text-gray-500">{{buildversion}}</span></h1>
            <p class="text-gray-600 dark:text-gray-300 mt-1">Real-time Ceph cluster monitoring<span id="cephVersionBadge" class="hidden ml-2 text-xs font-mono bg-gray-200 dark:bg-gray-700 text-gray-600 dark:text-gray-300 px-2 py-0.5 rounded-full align-middle"></span> · <a href="https://github.com/xorpaul/go-ceph-board" target="_blank" rel="noopener" class="text-xs text-blue-500 dark:text-blue-400 hover:underline">source</a></p>
        </div>
        <div class="flex items-center gap-4 flex-wrap">
            <div id="externalLinks" class="hidden flex items-center gap-2 flex-wrap"></div>
            <div id="clusterSwitcher" class="hidden flex items-center gap-2 flex-wrap"></div>
            <details id="docHeaderDetails" class="hidden relative">
                <summary class="cursor-pointer list-none text-xs font-semibold text-gray-600 dark:text-gray-300 bg-gray-200 dark:bg-gray-700 hover:bg-gray-300 dark:hover:bg-gray-600 px-3 py-1.5 rounded-lg select-none transition-colors">ℹ Info</summary>
                <div class="absolute right-0 top-full mt-1 z-50 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-xl shadow-xl p-3" style="min-width:32rem;max-width:60rem;">
                    <pre id="docHeaderContent" class="text-xs text-gray-700 dark:text-gray-300 whitespace-pre-wrap font-mono leading-relaxed m-0"></pre>
                </div>
            </details>
            <button id="themeToggle" class="p-2 rounded-lg bg-gray-200 dark:bg-gray-700 hover:bg-gray-300 dark:hover:bg-gray-600 transition-colors">
                <span id="themeIcon">🌙</span>
            </button>
        </div>
    </header>

    <!-- Production CephFS volume filter toggle — shown only when cluster has production_ceph_fs_volumes configured -->
    <div id="mdsFilterBar" class="hidden mb-4 flex items-center gap-4 flex-wrap">
        <button id="mdsFilterBtn" onclick="toggleMDSFilter()" style="display:inline-flex;align-items:center;gap:0.5rem;padding:0.55rem 1.4rem;border-radius:0.6rem;font-weight:700;font-size:0.95rem;border:2px solid #2563eb;background:#2563eb;color:#fff;cursor:pointer;transition:background 0.15s,border-color 0.15s;">
            <span>◉</span>
            <span id="mdsFilterBtnText">Production Only</span>
        </button>
        <span id="mdsFilterDesc" class="text-sm text-gray-500 dark:text-gray-400"></span>
    </div>

    <div id="connectionStatus" class="mb-4 text-sm font-medium"></div>

    <div id="dashboardContent" class="hidden">

        <!-- Quick Navigation -->
        <nav id="quickNav" class="flex flex-wrap gap-2 mb-4 sticky top-0 z-30 bg-gray-100 dark:bg-gray-900 py-2 px-1 border-b border-gray-200 dark:border-gray-700 -mx-1">
            <a href="#sec-health" class="nav-pill">Health</a>
            <a href="#sec-mds-mem" class="nav-pill">MDS Memory</a>
            <a href="#sec-mds-hosts-top" class="nav-pill">MDS Hosts</a>
            <a href="#sec-osds" class="nav-pill">OSD Hosts</a>
            <a href="#sec-monitors" class="nav-pill">Monitors</a>
            <a href="#sec-mds" class="nav-pill">MDS</a>
            <a href="#sec-pools" class="nav-pill">Pools</a>
            <a href="#sec-sankey" class="nav-pill">Session Map</a>
            <a href="#sec-mds-daemons" class="nav-pill">MDS Daemons</a>
            <a id="nav-mds-latency" href="#sec-mds-latency" class="nav-pill hidden">MDS Latency</a>
            <a id="nav-client-load" href="#sec-client-load" class="nav-pill hidden">Client Load</a>
            <a id="nav-mds-pending" href="#sec-mds-pending" class="nav-pill hidden">MDS Pending</a>
            <a id="nav-cap-ceiling" href="#sec-dg-cap-ceiling" class="nav-pill">Cap Ceiling</a>
            <a id="nav-mds-slow-ops" href="#sec-mds-slow-ops" class="nav-pill hidden">Slow Ops</a>
        </nav>

        <!-- Health Summary Cards -->
        <div id="sec-health" class="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-3 mb-4">

            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md flex items-center justify-between col-span-2 md:col-span-1">
                <div>
                    <p class="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Health</p>
                    <p id="healthStatus" class="text-xl font-bold text-gray-900 dark:text-white mt-1">-</p>
                    <p id="healthDetail" class="text-xs text-gray-500 dark:text-gray-400 mt-1 max-w-xs"></p>
                </div>
                <div id="healthDot" class="status-dot bg-gray-400 ml-2 flex-shrink-0"></div>
            </div>

            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">OSDs</p>
                <div class="flex items-baseline gap-1 mt-1">
                    <span id="osdUp" class="text-2xl font-bold text-green-600 dark:text-green-400">-</span>
                    <span class="text-xs text-gray-400">up</span>
                    <span id="osdIn" class="text-lg font-semibold text-blue-600 dark:text-blue-400 ml-1">-</span>
                    <span class="text-xs text-gray-400">in</span>
                </div>
                <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">of <span id="osdTotal">-</span> total</p>
                <div style="height:40px;position:relative;overflow:hidden;"><canvas id="osdSparkline"></canvas></div>
            </div>

            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">PGs</p>
                <div class="flex items-baseline gap-1 mt-1">
                    <span id="pgTotal" class="text-2xl font-bold text-gray-900 dark:text-white">-</span>
                    <span class="text-xs text-gray-400">total</span>
                </div>
                <div class="mt-1 space-y-0.5">
                    <div class="flex justify-between text-xs">
                        <span class="text-green-500">clean</span>
                        <span id="pgClean" class="font-mono font-bold text-green-500">-</span>
                    </div>
                    <div class="flex justify-between text-xs">
                        <span class="text-red-500">degraded</span>
                        <span id="pgDegraded" class="font-mono font-bold text-red-500">-</span>
                    </div>
                    <div class="flex justify-between text-xs">
                        <span class="text-orange-500">recovering</span>
                        <span id="pgRecovering" class="font-mono font-bold text-orange-500">-</span>
                    </div>
                    <div id="pgBackfillingRow" class="flex justify-between text-xs hidden">
                        <span class="text-amber-500">backfilling</span>
                        <span id="pgBackfilling" class="font-mono font-bold text-amber-500">-</span>
                    </div>
                    <div id="pgBackfillWaitRow" class="flex justify-between text-xs hidden">
                        <span class="text-amber-400">backfill wait</span>
                        <span id="pgBackfillWait" class="font-mono font-bold text-amber-400">-</span>
                    </div>
                    <div id="pgBackfillEtaRow" class="flex justify-between text-xs hidden">
                        <span class="text-amber-300">backfill eta</span>
                        <span id="pgBackfillEta" class="font-mono font-bold text-amber-300" title="Estimated time to finish backfill at current rate, based on last 120 s of PG-count samples">-</span>
                    </div>
                </div>
            </div>

            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Cluster Usage</p>
                <p id="clusterUsedPct" class="text-2xl font-bold text-gray-900 dark:text-white mt-1">-</p>
                <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">
                    <span id="clusterUsedBytes">-</span> / <span id="clusterTotalBytes">-</span>
                </p>
                <div style="height:40px;position:relative;overflow:hidden;"><canvas id="usageSparkline"></canvas></div>
            </div>

            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Stored Data<i class="th-info" style="margin-left:4px" data-tip="Sum of ceph_pool_stored across all pools — the logical client data before replication or erasure-coding overhead. Raw Used is ceph_cluster_total_used_raw_bytes (actual OSD consumption). Overhead shows how many raw bytes are written per client byte (e.g. ×3.0 for a pure 3-replica cluster).">i</i></p>
                <p id="clusterStoredBytes" class="text-2xl font-bold text-gray-900 dark:text-white mt-1">-</p>
                <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">raw <span id="clusterRawUsed2">-</span> &bull; overhead <span id="clusterStoredOverhead" class="font-mono font-bold">-</span></p>
            </div>

            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Monitors</p>
                <div class="flex items-baseline gap-1 mt-1">
                    <span id="monQuorum" class="text-2xl font-bold text-green-600 dark:text-green-400">-</span>
                    <span class="text-xs text-gray-400">in quorum</span>
                </div>
                <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">of <span id="monTotal">-</span> total</p>
            </div>

            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Objects</p>
                <p id="clusterObjects" class="text-2xl font-bold text-gray-900 dark:text-white mt-1">-</p>
                <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">raw used <span id="clusterRawUsed">-</span></p>
            </div>
        </div>

        <!-- MDS Active Site Indicator — collapsed by default, toggle to expand daemon detail -->
        <div id="mdsActiveSiteStrip" class="hidden mb-4 bg-white dark:bg-gray-800 p-3 rounded-xl shadow-md">
            <div class="flex flex-wrap items-center gap-3">
                <button id="mdsActiveSiteToggle" onclick="(function(){var d=document.getElementById('mdsActiveSiteDaemons');var t=document.getElementById('mdsActiveSiteToggle');var open=d.classList.toggle('hidden');t.innerHTML=open?'&#9654;&nbsp;MDS details':'&#9660;&nbsp;MDS details';})()" style="display:inline-flex;align-items:center;gap:0.25rem;padding:0.2rem 0.65rem;border-radius:0.4rem;font-weight:600;font-size:0.75rem;border:1.5px solid #2563eb;background:#2563eb;color:#fff;cursor:pointer;transition:background 0.15s,border-color 0.15s;flex-shrink:0">&#9654;&nbsp;MDS details</button>
                <div id="mdsActiveSitePills" class="flex flex-wrap gap-3"></div>
            </div>
            <div id="mdsActiveSiteDaemons" class="overflow-x-auto mt-1 hidden"></div>
        </div>

        <!-- MDS filesystem fullscreen modal -->
        <div id="mds-fs-modal" hidden style="position:fixed;inset:0;z-index:9000;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,0.6);backdrop-filter:blur(2px)" onclick="if(event.target===this)closeMdsFsModal()">
            <div style="background:var(--modal-bg,#fff);border-radius:10px;box-shadow:0 8px 48px rgba(0,0,0,0.35);width:85vw;height:92vh;display:flex;flex-direction:column;overflow:hidden">
                <div id="mds-fs-modal-header" style="display:flex;align-items:center;gap:10px;padding:10px 14px;border-bottom:1px solid var(--modal-border,#e5e7eb);flex-shrink:0">
                    <span id="mds-fs-modal-title" style="font-size:15px;font-weight:700;flex:1"></span>
                    <span id="mds-fs-modal-cluster" style="font-size:11px;opacity:0.55;margin-right:4px"></span>
                    <button onclick="closeMdsFsModal()" style="display:flex;align-items:center;justify-content:center;width:26px;height:26px;border-radius:5px;border:none;background:rgba(0,0,0,0.08);cursor:pointer;font-size:16px;line-height:1" title="Close (Esc)">✕</button>
                </div>
                <div id="mds-fs-modal-body" style="overflow:auto;flex:1;padding:8px 12px"></div>
            </div>
        </div>

        <!-- Prom-graph fullscreen modal -->
        <div id="prom-graph-modal" hidden style="position:fixed;inset:0;z-index:9000;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,0.6);backdrop-filter:blur(2px)" onclick="if(event.target===this)closePromGraphModal()">
            <div style="background:var(--modal-bg,#fff);border-radius:10px;box-shadow:0 8px 48px rgba(0,0,0,0.35);width:88vw;height:92vh;display:flex;flex-direction:column;overflow:hidden">
                <div id="prom-graph-modal-header" style="display:flex;align-items:center;gap:10px;padding:10px 14px;border-bottom:1px solid var(--modal-border,#e5e7eb);flex-shrink:0">
                    <div style="flex:1">
                        <div id="prom-graph-modal-title" style="font-size:15px;font-weight:700"></div>
                        <div id="prom-graph-modal-desc" style="font-size:11px;opacity:0.55;margin-top:2px"></div>
                    </div>
                    <span id="prom-graph-modal-cluster" style="font-size:11px;opacity:0.45;margin-right:4px"></span>
                    <button onclick="closePromGraphModal()" style="display:flex;align-items:center;justify-content:center;width:26px;height:26px;border-radius:5px;border:none;background:rgba(0,0,0,0.08);cursor:pointer;font-size:16px;line-height:1" title="Close (Esc)">✕</button>
                </div>
                <div id="prom-graph-modal-body" style="overflow:auto;flex:1;padding:12px 14px;display:flex;flex-direction:column;gap:12px"></div>
            </div>
        </div>

        <!-- Dynamic Prometheus graph panels — injected by initDynGraphs() from /clusters config -->
        <!-- sec-mds-trim lives here so it participates in the same 2-column grid -->
        <div id="dynGraphContainer" class="grid grid-cols-2 gap-4 mb-4">
            <section id="sec-mds-trim" class="bg-white dark:bg-gray-800 shadow rounded-lg p-4 hidden">
                <div class="mb-3">
                    <h2 class="text-base font-semibold text-gray-700 dark:text-gray-200">MDS Trim Rate by Filesystem</h2>
                    <p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">Inode LRU eviction rate summed per filesystem (delta of ceph_mds_inodes_expired/s)</p>
                </div>
                <div id="mdsTrimGrid" style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:0.375rem;margin-bottom:0.5rem"></div>
                <div class="relative" style="height:120px"><canvas id="mdsTrimChart"></canvas></div>
                <div id="mdsTrimLegend" class="mt-2"></div>
            </section>
        </div>

        <!-- Charts Row -->
        <div id="sec-charts" class="grid grid-cols-1 lg:grid-cols-3 gap-4 mb-4">
            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white mb-2">PG States Over Time</h3>
                <div style="height:150px;position:relative;overflow:hidden;">
                    <canvas id="pgChart"></canvas>
                </div>
                <div id="pgChartLegend" class="mt-2"></div>
            </div>
            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white mb-2">OSD Status Over Time</h3>
                <div style="height:150px;position:relative;overflow:hidden;">
                    <canvas id="osdChart"></canvas>
                </div>
                <div id="osdChartLegend" class="mt-2"></div>
            </div>
            <div class="metric-card bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md">
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white mb-2">Cluster Usage Over Time</h3>
                <div style="height:150px;position:relative;overflow:hidden;">
                    <canvas id="usageChart"></canvas>
                </div>
                <div id="usageChartLegend" class="mt-2"></div>
            </div>
        </div>

        <!-- MDS Host Memory Breakdown -->
        <div id="sec-mds-mem" class="bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md mb-4">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-1">MDS Host Memory Breakdown</h3>
            <p class="text-xs text-gray-500 dark:text-gray-400 mb-3">Per-daemon RSS from <code>ceph_mds_mem_rss</code>. Active daemons are coloured by filesystem; standby-replay daemons are shown at reduced opacity. Inactive/standby-pool (moveable) daemons are excluded — they consume negligible memory and their systemd-reported RSS is unreliable; use <code>ceph tell mds.&lt;id&gt; heap stats</code> if needed.</p>
            <div id="mdsMemChartsGrid" class="grid grid-cols-2 md:grid-cols-3 gap-4">
                <p class="col-span-3 text-center text-gray-400 py-4">Loading MDS memory data...</p>
            </div>
        </div>

        <!-- MDS Hosts (summary before OSD table) -->
        <div id="sec-mds-hosts-top" class="bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md mb-4">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-3">MDS Hosts</h3>
            <div class="overflow-x-auto">
                <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                    <thead class="bg-gray-50 dark:bg-gray-700">
                        <tr>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Host<i class="th-info" data-tip="Hostname of the MDS server. The colored left border indicates the site.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Site<i class="th-info" data-tip="Physical datacenter site inferred from the hostname.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">MDS<i class="th-info" data-tip="Active MDS rank assignments per filesystem on this host, with session counts. Each badge shows filesystem name × active ranks · sessions. Grey text shows standby daemon count.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase" style="min-width:80px">CPU<i class="th-info" data-tip="CPU utilization from node_exporter. Color: green &lt; 50%, yellow &ge; 50%, orange &ge; 75%, red &ge; 90%.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase" style="min-width:80px">Mem<i class="th-info" data-tip="Memory usage from node_exporter. Color: green &lt; 70%, yellow &ge; 70%, orange &ge; 80%, red &ge; 90%.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Uptime<i class="th-info" data-tip="System uptime from node_exporter.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Svc Uptime<i class="th-info" data-tip="Time since the most recently restarted Ceph MDS daemon on this host entered active state, from the systemd textfile collector.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">OS<i class="th-info" data-tip="Operating system from node_exporter.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Ceph Ver<i class="th-info" data-tip="Ceph version of the MDS daemons on this host, from ceph_mds_metadata. Highlighted amber when the version differs from the cluster majority — indicates this host is mid-upgrade.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Sessions<i class="th-info" data-tip="Total client sessions on all active MDS daemons on this host.">i</i></th>
                        </tr>
                    </thead>
                    <tbody id="mdsHostsTopTableBody" class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                        <tr><td colspan="10" class="px-3 py-6 text-center text-gray-500">Loading MDS hosts...</td></tr>
                    </tbody>
                </table>
            </div>
        </div>

        <!-- OSD Host Table -->
        <div id="sec-osds" class="bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md mb-4">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-3">OSD Hosts</h3>
            <div class="overflow-x-auto">
                <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                    <thead class="bg-gray-50 dark:bg-gray-700">
                        <tr>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Host<i class="th-info" data-tip="Hostname of the OSD server. The colored left border indicates the site this host belongs to.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Site<i class="th-info" data-tip="Physical datacenter site, matched from the hostname by the sites list in the config. Used to visualize stretch-cluster balance across sites.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">OSDs<i class="th-info" data-tip="OSD state summary: up/in/total. Red if any OSD is down, orange if any OSD is up-but-out (excluded from data placement). All green means every OSD on this host is healthy.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase" style="min-width:110px">Disk Usage<i class="th-info" data-tip="Aggregate raw disk fill across all OSDs on this host as a percentage of total raw capacity. Color: green &lt; 70%, yellow &ge; 70%, orange &ge; 80%, red &ge; 90%.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Used<i class="th-info" data-tip="Total raw bytes consumed across all OSDs on this host (sum of ceph_osd_stat_bytes_used). Raw device-level usage — not logical pool usage; includes replication and erasure overhead.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Total<i class="th-info" data-tip="Total raw disk capacity of all OSDs on this host (sum of ceph_osd_stat_bytes). Formatted in binary units (GiB / TiB).">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Apply ms<i class="th-info" data-tip="Average apply latency in milliseconds across all OSDs on this host. Apply latency is the time to apply an operation to the in-memory store. Color: green &lt; 10ms, yellow &ge; 10ms, orange &ge; 20ms, red &ge; 50ms.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Commit ms<i class="th-info" data-tip="Average commit latency in milliseconds across all OSDs on this host. Commit latency measures the time to flush an operation to the journal or WAL. High values typically indicate slow disks or I/O saturation.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase" style="min-width:80px">CPU<i class="th-info" data-tip="CPU utilization computed from node_exporter node_cpu_seconds_total counters (idle time subtracted from total). Averaged across all CPUs on the host. Color: green &lt; 50%, yellow &ge; 50%, orange &ge; 75%, red &ge; 90%. Shows — on first poll (needs two samples to compute a rate).">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase" style="min-width:80px">Mem<i class="th-info" data-tip="Memory usage: (MemTotal − MemAvailable) / MemTotal from node_exporter. Color: green &lt; 70%, yellow &ge; 70%, orange &ge; 80%, red &ge; 90%.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Uptime<i class="th-info" data-tip="System uptime derived from node_exporter node_boot_time_seconds. Shows how long the host has been running since its last boot.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Svc Uptime<i class="th-info" data-tip="Time since the most recently restarted Ceph OSD daemon on this host entered active state, from the systemd textfile collector. Shows — if the helper script has not run yet.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">OS<i class="th-info" data-tip="Operating system from node_exporter node_os_info (pretty_name), falling back to node_uname_info sysname+release if os_info is unavailable. Hover for the full string.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Ceph Ver<i class="th-info" data-tip="Ceph version of the OSD daemons on this host, from ceph_osd_metadata. Highlighted amber when the version differs from the cluster majority — indicates this host is mid-upgrade.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">OSD Status<i class="th-info" data-tip="Color-coded badge per OSD on this host. Green = up+in (healthy), orange = up+out (excluded from placement), red = down+out (failed), yellow = down+in (unusual). Hover a badge for the daemon name and state.">i</i></th>
                        </tr>
                    </thead>
                    <tbody id="osdHostTableBody" class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                        <tr><td colspan="15" class="px-3 py-6 text-center text-gray-500">Loading OSD hosts...</td></tr>
                    </tbody>
                </table>
            </div>
        </div>

        <!-- Monitors (full width) -->
        <div id="sec-monitors" class="bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md mb-4">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-3">Monitors</h3>
            <div class="overflow-x-auto">
                <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                    <thead class="bg-gray-50 dark:bg-gray-700">
                        <tr>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Monitor<i class="th-info" data-tip="Ceph monitor daemon name. Monitors maintain the cluster map and participate in the Paxos quorum for consensus on all cluster state changes.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Hostname<i class="th-info" data-tip="Hostname of the server running this monitor daemon.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Site<i class="th-info" data-tip="Physical datacenter site inferred from the hostname. A healthy stretch cluster distributes monitors across sites so that no single site failure breaks quorum.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Ceph Ver<i class="th-info" data-tip="Ceph version of this monitor daemon, from ceph_mon_metadata. Highlighted amber when the version differs from the cluster majority — indicates this monitor is mid-upgrade.">i</i></th>
                            <th class="px-3 py-2 text-center text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Quorum<i class="th-info" data-tip="Whether this monitor is currently participating in the Paxos quorum. A monitor outside quorum cannot vote on cluster map changes. Loss of quorum majority causes all client write operations to stall.">i</i></th>
                            <th class="px-3 py-2 text-center text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Sessions<i class="th-info" data-tip="Active client sessions on this monitor (ceph_mon_num_sessions). Sessions are distributed across all monitors and do not reliably identify the Paxos leader — clients connect to any monitor, not just the leader.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase" style="min-width:80px">CPU<i class="th-info" data-tip="CPU utilization from node_exporter (rate of non-idle CPU seconds). Shows — on first poll.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase" style="min-width:80px">Mem<i class="th-info" data-tip="Memory usage: (MemTotal − MemAvailable) / MemTotal from node_exporter.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Store<i class="th-info" data-tip="RocksDB store size on disk (ceph_mon_store_size). The monitor database holds the cluster map history and OSD maps. Runaway growth (multi-GiB) indicates compaction problems or a very long cluster history.">i</i></th>
                            <th class="px-3 py-2 text-center text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Quorum Age<i class="th-info" data-tip="Time since this monitor last participated in a quorum change (ceph_mon_quorum_age). A very short age means the monitor recently (re)joined or an election just completed.">i</i></th>
                            <th class="px-3 py-2 text-center text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Clock Skew<i class="th-info" data-tip="Maximum clock skew vs peers (ceph_mon_timecheck_skew). Ceph requires all monitor clocks within 50 ms of each other — above that, the monitor may be kicked out of quorum. Green &lt;10ms, yellow 10–50ms, red ≥50ms.">i</i></th>
                            <th class="px-3 py-2 text-center text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Peer RTT<i class="th-info" data-tip="Maximum round-trip latency to peer monitors (ceph_mon_timecheck_latency). High latency between monitors increases Paxos commit times and can cause election instability under load.">i</i></th>
                            <th class="px-3 py-2 text-center text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Elections<i class="th-info" data-tip="Cumulative election results since daemon start: wins / losses / total calls from ceph_mon_election_win, ceph_mon_election_lose, ceph_mon_election_call. ★ marks the monitor with the most wins — the best available proxy for the current Paxos leader. Frequent elections indicate instability.">i</i></th>
                        </tr>
                    </thead>
                    <tbody id="monTableBody" class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                        <tr><td colspan="13" class="px-3 py-6 text-center text-gray-500">Loading monitors...</td></tr>
                    </tbody>
                </table>
            </div>
        </div>

        <!-- MDS Filesystems + MDS Hosts (combined card) -->
        <div id="sec-mds" class="bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md mb-4">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-3">MDS (Metadata Servers)</h3>
            <div class="overflow-x-auto mb-6">
                <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                    <thead class="bg-gray-50 dark:bg-gray-700">
                        <tr>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Filesystem<i class="th-info" data-tip="Name of the CephFS filesystem. A Ceph cluster can host multiple independent filesystems, each with its own pool layout and MDS daemons.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">FS ID<i class="th-info" data-tip="Numeric filesystem ID assigned by the Ceph monitor when the filesystem was created. Stable across renames.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Active<i class="th-info" data-tip="Number of active MDS ranks currently serving metadata for this filesystem.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Ranks<i class="th-info" data-tip="Active MDS rank numbers assigned to this filesystem.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Sites<i class="th-info" data-tip="Physical sites hosting active MDS daemons for this filesystem, with per-site counts.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Sessions<i class="th-info" data-tip="Total client sessions across all active MDS daemons for this filesystem, from ceph_mds_sessions_session_count.">i</i></th>
                        </tr>
                    </thead>
                    <tbody id="mdsTableBody" class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                        <tr><td colspan="6" class="px-3 py-6 text-center text-gray-500">Loading MDS...</td></tr>
                    </tbody>
                </table>
            </div>
        </div>
        </div>

        <!-- Pool Table -->
        <div id="sec-pools" class="bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md mb-4">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-3">Pools</h3>
            <div class="overflow-x-auto">
                <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                    <thead class="bg-gray-50 dark:bg-gray-700">
                        <tr>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('name')">Pool<span id="psort-name"></span><i class="th-info" data-tip="Pool name and numeric ID. Pools are the primary storage namespace in Ceph. Each pool has its own replication or erasure-coding policy and can be assigned quotas independently.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Type<i class="th-info" style="margin-left:4px" data-tip="Pool type and redundancy scheme from ceph_pool_metadata. Replicated pools store N full copies (rep×N). Erasure-coded pools split data into k data chunks and m coding chunks (ec k+m); total raw overhead is (k+m)/k times the logical size.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('stored')">Stored<span id="psort-stored"></span><i class="th-info" data-tip="Client data stored in this pool (ceph_pool_stored) — the STORED column from ceph df. This is the actual application data before replication or erasure-coding overhead. Smaller than Used because it excludes RADOS metadata and other internal overhead.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('overhead')">Overhead<span id="psort-overhead"></span><i class="th-info" data-tip="Raw OSD bytes consumed by this pool (ceph_pool_stored_raw) divided by the client data stored (ceph_pool_stored). A rep×3 pool shows ×3.00; an EC k=4 m=2 pool shows ×1.50. Shows — when stored data is zero (empty pool).">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('used')">Used<span id="psort-used"></span><i class="th-info" data-tip="Logical bytes used by this pool (ceph_pool_bytes_used) — the USED column from ceph df. Slightly larger than Stored because it includes RADOS metadata and internal pool overhead in addition to client data.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('avail')">Available<span id="psort-avail"></span><i class="th-info" data-tip="Remaining usable logical space in this pool. Pools share the cluster's OSD capacity, so available figures across pools overlap — they cannot be summed to get total free space. A pool can also be individually limited by a quota.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" style="min-width:120px" onclick="setPoolSort('pct')">% Used<span id="psort-pct"></span><i class="th-info" data-tip="Used / (Used + Available) expressed as a percentage. A pool can fill independently of the overall cluster if it has a quota or if its OSDs fill before others. Color: green &lt; 70%, yellow &ge; 70%, orange &ge; 80%, red &ge; 90%.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('rdIops')">Rd/s<span id="psort-rdIops"></span><i class="th-info" data-tip="Read operations per second (IOPS). Computed as the delta of the ceph_pool_rd counter divided by the time elapsed since the previous refresh. First refresh always shows 0.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('wrIops')">Wr/s<span id="psort-wrIops"></span><i class="th-info" data-tip="Write operations per second (IOPS). Computed as the delta of the ceph_pool_wr counter divided by the time elapsed since the previous refresh. First refresh always shows 0.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('rdMBps')">Rd MB/s<span id="psort-rdMBps"></span><i class="th-info" data-tip="Read throughput in MiB per second. Derived from the delta of the ceph_pool_rd_bytes cumulative counter between refresh intervals. Reflects actual bytes transferred to clients, before any decompression.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('wrMBps')">Wr MB/s<span id="psort-wrMBps"></span><i class="th-info" data-tip="Write throughput in MiB per second. Derived from the delta of the ceph_pool_wr_bytes cumulative counter between refresh intervals. Reflects bytes written by clients; internal recovery and replication traffic is not included.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase cursor-pointer select-none hover:text-gray-900 dark:hover:text-white" onclick="setPoolSort('objs')">Objects<span id="psort-objs"></span><i class="th-info" data-tip="Number of RADOS objects in this pool. Ceph splits files into objects (typically 4 MiB each). Large counts indicate many small objects (e.g. RGW) or large files; RBD volumes typically show relatively few objects per GiB.">i</i></th>
                        </tr>
                    </thead>
                    <tbody id="poolTableBody" class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                        <tr><td colspan="12" class="px-3 py-6 text-center text-gray-500">Loading pools...</td></tr>
                    </tbody>
                </table>
            </div>
        </div>

        <!-- MDS Session Distribution (Sankey) -->
        <div id="sec-sankey" class="bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md mb-4">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-3">MDS Session Distribution</h3>
            <div id="mdsSankeyContainer"></div>
            <div class="mt-3 flex flex-wrap gap-x-6 gap-y-1 text-xs text-gray-500 dark:text-gray-400 border-t border-gray-100 dark:border-gray-700 pt-2">
                <span><span style="display:inline-block;width:10px;height:10px;border-radius:2px;background:#6366f1;vertical-align:middle;margin-right:4px"></span>Left bars = CephFS filesystems — label is the fs name, the small number to the right is total sessions on that filesystem</span>
                <span><span style="display:inline-block;width:10px;height:10px;border-radius:2px;background:#0ea5e9;vertical-align:middle;margin-right:4px"></span>Right bars = MDS host servers — label is the hostname, the small number to the left is total sessions served by that host</span>
                <span><span style="display:inline-block;width:20px;height:8px;border-radius:1px;background:rgba(99,102,241,0.35);vertical-align:middle;margin-right:4px"></span>Flow band width ∝ session count between that filesystem and host — hover a band for the exact number</span>
            </div>
        </div>

        <!-- MDS Daemons -->
        <div id="sec-mds-daemons" class="bg-white dark:bg-gray-800 p-4 rounded-xl shadow-md mb-4">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-3">MDS Daemons</h3>
            <div class="overflow-x-auto">
                <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                    <thead class="bg-gray-50 dark:bg-gray-700">
                        <tr>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Filesystem<i class="th-info" data-tip="CephFS filesystem this daemon is serving.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Rank<i class="th-info" data-tip="MDS rank assigned to this daemon. Each rank is an independent partition of the metadata namespace. Lower ranks handle the root of the directory tree.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Daemon<i class="th-info" data-tip="ceph_daemon label from ceph_mds_metadata, in the form mds.m&lt;id&gt;-&lt;hostname&gt;.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Host<i class="th-info" data-tip="Server running this MDS daemon.">i</i></th>
                            <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Site<i class="th-info" data-tip="Physical datacenter site of the host.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Sessions<i class="th-info" data-tip="Number of client sessions currently open on this MDS daemon, from ceph_mds_sessions_session_count. Each CephFS client that has contacted this rank holds one session here.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Reqs/s<i class="th-info" data-tip="Client request rate (ceph_mds_request delta per second). This is the ACTIVITY column in 'ceph fs status'. First refresh shows 0.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">DNS<i class="th-info" data-tip="Dentry (directory name) count in the MDS cache (ceph_mds_mem_dn). Matches the DNS column in 'ceph fs status'. High values mean a large fraction of the namespace is hot in memory.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">INOS<i class="th-info" data-tip="Total inode count in the MDS cache (ceph_mds_inodes). Matches the INOS column in 'ceph fs status'.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">DIRS<i class="th-info" data-tip="Directory fragment count in the MDS cache (ceph_mds_mem_dir). Matches the DIRS column in 'ceph fs status'.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">CAPS<i class="th-info" data-tip="Capabilities held in memory on this daemon (ceph_mds_mem_cap). Matches the CAPS column in 'ceph fs status'. Includes caps for all clients across all inodes.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Inodes w/ Caps<i class="th-info" data-tip="Inodes for which this MDS has issued capabilities to at least one client (ceph_mds_inodes_with_caps). Growing values under memory pressure indicate the MDS is holding many client leases and may need to revoke aggressively.">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Slow/s<i class="th-info" data-tip="Rate of MDS slow replies per second, derived from the delta of ceph_mds_slow_reply between refresh intervals. Zero is normal; any non-zero value means the MDS is struggling to respond in time, which can stall CephFS clients (symptom of cap-pressure, as seen in INC-129441).">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">RSS<i class="th-info" data-tip="Resident memory of the MDS daemon process (ceph_mds_mem_rss). Shows — when the mgr is running with exclude_perf_counters = true (common on US clusters).">i</i></th>
                            <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-300 uppercase">Trim/s<i class="th-info" data-tip="LRU inode eviction rate (delta of ceph_mds_inodes_expired/s). Non-zero means the MDS is trimming cold inodes to stay within mds_cache_memory_limit. High rates (≥1k/s) combined with caps at ceiling indicate the memory limit needs raising.">i</i></th>
                        </tr>
                    </thead>
                    <tbody id="mdsDaemonTableBody" class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                        <tr><td colspan="15" class="px-3 py-6 text-center text-gray-500">Loading MDS daemons...</td></tr>
                    </tbody>
                </table>
            </div>
        </div>


    </div><!-- end dashboardContent -->
</div>

`
