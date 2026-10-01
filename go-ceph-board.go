package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	yaml "gopkg.in/yaml.v3"
)

//go:embed static
var staticDir embed.FS

type TLSConfig struct {
	Enabled        bool     `yaml:"enabled"`
	CAFile         string   `yaml:"ca_file"`
	CertFile       string   `yaml:"cert_file"`
	KeyFile        string   `yaml:"key_file"`
	AllowedCNs     []string `yaml:"allowed_cns"`
	SkipClientAuth bool     `yaml:"skip_client_auth"`
	// Optional links on the 401/403 pages: where users get a client
	// certificate, and where they request access.
	CertHelpURL     string `yaml:"cert_help_url"`
	AccessHelpURL   string `yaml:"access_help_url"`
	HealthCheckPort string `yaml:"health_check_port"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
	Port    string `yaml:"port"`
}

// PromGraphConfig is one configurable Prometheus-query graph panel.
type PromGraphConfig struct {
	ID               string   `yaml:"id"`
	Title            string   `yaml:"title"`
	Description      string   `yaml:"description"`
	Query            string   `yaml:"query"`
	PrometheusURL    string   `yaml:"prometheus_url"`
	MaxEntries       int      `yaml:"max_entries"`
	Note             string   `yaml:"note"`
	Warn             *float64 `yaml:"warn"`
	Crit             *float64 `yaml:"crit"`
	ResolvePtr       bool     `yaml:"resolve_ptr"`
	ProductionFilter bool     `yaml:"production_filter"`
	FullWidth        bool     `yaml:"full_width"`
	Unit             string   `yaml:"unit"`
	ColorRelative    bool     `yaml:"color_relative"`
	Integer          bool     `yaml:"integer"`
	AlwaysShow       bool     `yaml:"always_show"`
}

// ClusterConfig holds per-cluster connection settings.
type ClusterConfig struct {
	MgrPrometheusURLs       []string          `yaml:"mgr_prometheus_urls"`
	NodeExporterPort        string            `yaml:"node_exporter_port"`
	NodeExporterHostSuffix  string            `yaml:"node_exporter_host_suffix"`
	MDSHosts                []string          `yaml:"mds_hosts"`
	ExtraHosts              []string          `yaml:"extra_hosts"`
	PrometheusURL           string            `yaml:"prometheus_url"`
	ProductionCephFSVolumes []string          `yaml:"production_ceph_fs_volumes"`
	PromGraphs              []PromGraphConfig `yaml:"prom_graphs"`
}

// ThresholdPair holds warn and crit boundary values for a single metric.
// Values below Warn are shown green, Warn≤v<Crit amber, Crit≤v red.
type ThresholdPair struct {
	Warn float64 `yaml:"warn" json:"warn"`
	Crit float64 `yaml:"crit" json:"crit"`
}

// ThresholdsConfig holds optional threshold boundaries for dashboard metrics.
// Nil fields are replaced by defaults in clustersHandler so the UI always has values.
type ThresholdsConfig struct {
	UsagePct *ThresholdPair `yaml:"usage_pct" json:"usage_pct,omitempty"`
}

// CephConfig holds global Ceph settings plus a map of named cluster configs.
// Named clusters are arbitrary keys under `ceph:` in the YAML (e.g. EU, US).
// Known scalar keys (insecure_skip_verify, ca_file) are decoded normally;
// all other mapping keys are treated as cluster configs.
type CephConfig struct {
	InsecureSkipVerify bool                     `yaml:"insecure_skip_verify"`
	CAFile             string                   `yaml:"ca_file"`
	Clusters           map[string]ClusterConfig // populated by UnmarshalYAML
	ClusterOrder       []string                 // config-file order, set by UnmarshalYAML
}

func (c *CephConfig) UnmarshalYAML(value *yaml.Node) error {
	knownKeys := map[string]bool{
		"insecure_skip_verify": true,
		"ca_file":              true,
	}

	type plain struct {
		InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		CAFile             string `yaml:"ca_file"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	c.InsecureSkipVerify = p.InsecureSkipVerify
	c.CAFile = p.CAFile

	if value.Kind == yaml.MappingNode {
		c.Clusters = map[string]ClusterConfig{}
		for i := 0; i+1 < len(value.Content); i += 2 {
			key := value.Content[i].Value
			if knownKeys[key] {
				continue
			}
			var clusterCfg ClusterConfig
			if err := value.Content[i+1].Decode(&clusterCfg); err != nil {
				return fmt.Errorf("cluster %q: %v", key, err)
			}
			c.Clusters[key] = clusterCfg
			c.ClusterOrder = append(c.ClusterOrder, key)
		}
	}
	return nil
}

// ExternalLink is an optional labeled hyperlink shown in the dashboard header.
type ExternalLink struct {
	Label string `yaml:"label" json:"label"`
	URL   string `yaml:"url"   json:"url"`
}

// Site maps a substring of a hostname (or of its CRUSH location) to the
// datacenter label shown in the host tables. Sites are checked in order;
// Color/Bg default to a built-in palette by position.
type Site struct {
	Match string `yaml:"match" json:"match"`
	Label string `yaml:"label" json:"label"`
	Color string `yaml:"color" json:"color,omitempty"`
	Bg    string `yaml:"bg"    json:"bg,omitempty"`
}

// HostLink adds a small link next to every hostname, e.g. to a CMDB or
// inventory search. "{host}" in URL is replaced by the URL-encoded hostname.
type HostLink struct {
	URL   string `yaml:"url"   json:"url"`
	Title string `yaml:"title" json:"title"`
}

type Config struct {
	Server         ServerConfig     `yaml:"server"`
	TLS            TLSConfig        `yaml:"tls"`
	Ceph           CephConfig       `yaml:"ceph"`
	Sites          []Site           `yaml:"sites"`
	HostLink       *HostLink        `yaml:"host_link"`
	ExternalLinks  []ExternalLink   `yaml:"external_links"`
	Thresholds     ThresholdsConfig `yaml:"thresholds"`
	DocumentHeader string           `yaml:"document_header"`
}

// clusterState holds runtime state for a single named Ceph cluster.
type clusterState struct {
	name            string
	cfg             ClusterConfig
	activeMgrURL    string
	cephVersion     string // protected by activeMgrMu; short form e.g. "v18.2.7 (reef)"
	activeMgrMu     sync.RWMutex
	mgrMetrics      *metricsCache
	nodeMetrics     *metricsCache
	promGraphCaches map[string]*metricsCache
}

// CertificateManager handles automatic reloading of TLS certificates
type CertificateManager struct {
	certFile    string
	keyFile     string
	caFile      string
	certificate *tls.Certificate
	caCertPool  *x509.CertPool
	mutex       sync.RWMutex
	watcher     *fsnotify.Watcher
	logger      *log.Logger
}

// metricsCache holds the last successful /metrics fetch and serialises
// concurrent refreshes so only one upstream request is in-flight at a time.
// All other callers wait for it and receive the same result.
type metricsCache struct {
	mu        sync.Mutex
	body      []byte
	lastErr   error // last fetch error, returned to waiters when the in-flight fetch fails
	fetchedAt time.Time
	ttl       time.Duration
	fetching  bool
	done      chan struct{} // closed when the in-flight fetch completes
}

func (c *metricsCache) get(fetch func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if time.Since(c.fetchedAt) < c.ttl {
		body := c.body
		c.mu.Unlock()
		return body, nil
	}
	if c.fetching {
		done := c.done
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		body, err := c.body, c.lastErr
		c.mu.Unlock()
		return body, err
	}
	c.fetching = true
	c.done = make(chan struct{})
	c.mu.Unlock()

	body, err := fetch()

	c.mu.Lock()
	c.lastErr = err
	if err == nil {
		c.body = body
		c.fetchedAt = time.Now()
	}
	done := c.done
	c.fetching = false
	c.done = nil
	c.mu.Unlock()
	close(done)

	return body, err
}

// nodeMetricNames is the set of node_exporter metric families we keep.
// All other metric lines (and their # HELP/# TYPE headers) are discarded
// to keep the aggregated response small.
var nodeMetricNames = map[string]bool{
	"node_cpu_seconds_total":            true,
	"node_cpu_info":                     true,
	"node_memory_MemTotal_bytes":        true,
	"node_memory_MemAvailable_bytes":    true,
	"node_boot_time_seconds":            true,
	"node_uname_info":                   true,
	"node_os_info":                      true,
	"ceph_daemon_memory_bytes":          true,
	"ceph_daemon_start_time_seconds":    true,
	"ceph_mds_rank_incarnation":         true,
	"ceph_mds_rank_state_seq":           true,
	"ceph_mds_rank_assigned":            true,
	"ceph_mdsmap_epoch":                 true,
	"ceph_mds_cache_memory_limit_bytes": true,
	"ceph_mds_sr_lag_bytes":             true,
	"ceph_mds_sr_margin_bytes":          true,
	"ceph_mds_journal_live_bytes":       true,
	"ceph_mds_sr_present":               true,
}

// reNodeHostname extracts the hostname= label from ceph_osd_metadata /
// ceph_mds_metadata lines. Both families use the same label name.
var reNodeHostname = regexp.MustCompile(`[,{]hostname="([^"]+)"`)

// rePublicAddr extracts the first IPv4 from a public_addr= label value.
// Handles both "1.2.3.4:port/nonce" and "[v2:1.2.3.4:port/nonce,...]" formats.
var rePublicAddrIP = regexp.MustCompile(`public_addr="[^"]*?(\d+\.\d+\.\d+\.\d+)`)

// reCephVersion matches the version number and codename in ceph_*_metadata
// ceph_version label values, e.g. "ceph version 18.2.7 (hash) reef (stable)".
var reCephVersion = regexp.MustCompile(`ceph version (\S+) \([^)]+\) (\w+)`)

var (
	buildversion  string
	buildtime     string
	debug         bool
	verbose       bool
	config        Config
	certManager   *CertificateManager
	cephClient    *http.Client
	clusterStates map[string]*clusterState
	clusterOrder  []string
)

// NewCertificateManager creates a new certificate manager with file watching
func NewCertificateManager(certFile, keyFile, caFile string) (*CertificateManager, error) {
	cm := &CertificateManager{
		certFile: certFile,
		keyFile:  keyFile,
		caFile:   caFile,
		logger:   log.New(os.Stdout, "[CertManager] ", log.LstdFlags),
	}

	if err := cm.loadCertificates(); err != nil {
		return nil, fmt.Errorf("failed to load initial certificates: %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %v", err)
	}
	cm.watcher = watcher

	if err := cm.watchFiles(); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("failed to watch certificate files: %v", err)
	}

	go cm.watchForChanges()
	cm.logger.Printf("Certificate manager initialized, watching: %s, %s, %s", certFile, keyFile, caFile)
	return cm, nil
}

func (cm *CertificateManager) loadCertificates() error {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	cert, err := tls.LoadX509KeyPair(cm.certFile, cm.keyFile)
	if err != nil {
		return fmt.Errorf("failed to load server certificate: %v", err)
	}
	cm.certificate = &cert

	caCert, err := os.ReadFile(cm.caFile)
	if err != nil {
		return fmt.Errorf("failed to read CA file: %v", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to parse CA certificate")
	}
	cm.caCertPool = caCertPool

	cm.logger.Printf("Certificates loaded successfully")
	return nil
}

func (cm *CertificateManager) watchFiles() error {
	files := []string{cm.certFile, cm.keyFile, cm.caFile}
	for _, file := range files {
		dir := filepath.Dir(file)
		if err := cm.watcher.Add(dir); err != nil {
			return fmt.Errorf("failed to watch directory %s: %v", dir, err)
		}
		if err := cm.watcher.Add(file); err != nil {
			cm.logger.Printf("Warning: failed to watch file %s directly: %v", file, err)
		}
	}
	return nil
}

func (cm *CertificateManager) watchForChanges() {
	defer cm.watcher.Close()

	debounceTimer := time.NewTimer(0)
	if !debounceTimer.Stop() {
		<-debounceTimer.C
	}

	for {
		select {
		case event, ok := <-cm.watcher.Events:
			if !ok {
				return
			}
			if cm.isRelevantFile(event.Name) {
				if debug {
					cm.logger.Printf("File system event: %s %s", event.Op.String(), event.Name)
				}
				debounceTimer.Reset(500 * time.Millisecond)
			}

		case err, ok := <-cm.watcher.Errors:
			if !ok {
				return
			}
			cm.logger.Printf("Watcher error: %v", err)

		case <-debounceTimer.C:
			cm.logger.Printf("Certificate files changed, reloading...")
			if err := cm.loadCertificates(); err != nil {
				cm.logger.Printf("Failed to reload certificates: %v", err)
			} else {
				cm.logger.Printf("Certificates reloaded successfully")
			}
		}
	}
}

func (cm *CertificateManager) isRelevantFile(filePath string) bool {
	files := []string{cm.certFile, cm.keyFile, cm.caFile}
	for _, file := range files {
		if filePath == file || filepath.Base(filePath) == filepath.Base(file) {
			return true
		}
	}
	return false
}

func (cm *CertificateManager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.certificate, nil
}

func (cm *CertificateManager) GetCACertPool() *x509.CertPool {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.caCertPool
}

func (cm *CertificateManager) Close() error {
	if cm.watcher != nil {
		return cm.watcher.Close()
	}
	return nil
}

// buildCephClient constructs the HTTP client used for all Ceph Prometheus requests
func buildCephClient() error {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: config.Ceph.InsecureSkipVerify,
	}

	if config.Ceph.CAFile != "" {
		caCert, err := os.ReadFile(config.Ceph.CAFile)
		if err != nil {
			return fmt.Errorf("ceph ca_file: %v", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("ceph ca_file: no valid certificates found")
		}
		tlsCfg.RootCAs = pool
	}

	cephClient = &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
		Timeout:   30 * time.Second,
	}
	return nil
}

// getClusterState resolves ?cluster= to a clusterState. Missing/empty → first cluster.
func getClusterState(r *http.Request) (*clusterState, string, error) {
	name := r.URL.Query().Get("cluster")
	if name == "" && len(clusterOrder) > 0 {
		name = clusterOrder[0]
	}
	st, ok := clusterStates[name]
	if !ok {
		return nil, name, fmt.Errorf("unknown cluster %q", name)
	}
	return st, name, nil
}

// warmupCaches pre-populates the mgr and node metrics caches for all clusters
// in background goroutines so the first user request doesn't incur cold-start latency.
// Clusters are fetched in parallel; within each cluster mgr metrics are fetched first
// since node metrics discovery depends on them.
func warmupCaches() {
	for _, name := range clusterOrder {
		name := name
		st := clusterStates[name]
		go func() {
			log.Printf("cluster %s: cache warmup: fetching mgr metrics", name)
			_, err := st.mgrMetrics.get(func() ([]byte, error) {
				return fetchActiveMgrMetrics(st)
			})
			if err != nil {
				log.Printf("cluster %s: cache warmup: mgr metrics failed: %v", name, err)
				return
			}
			log.Printf("cluster %s: cache warmup: fetching node metrics", name)
			_, err = st.nodeMetrics.get(func() ([]byte, error) {
				return fetchAndAggregateNodeMetrics(st)
			})
			if err != nil {
				log.Printf("cluster %s: cache warmup: node metrics failed: %v", name, err)
				return
			}
			log.Printf("cluster %s: cache warmup: complete", name)
		}()

		for _, g := range st.cfg.PromGraphs {
			g := g
			go func() {
				log.Printf("cluster %s: cache warmup: fetching prom-graph %q", name, g.ID)
				cache := st.promGraphCaches[g.ID]
				if cache == nil {
					return
				}
				if _, err := cache.get(func() ([]byte, error) {
					return fetchPromGraph(st, g)
				}); err != nil {
					log.Printf("cluster %s: cache warmup: prom-graph %q failed: %v", name, g.ID, err)
				}
			}()
		}
	}
}

// checkMgrURLsReachable probes each configured mgr Prometheus URL at startup
// and logs whether each one responded. Does not abort startup on failure.
func checkMgrURLsReachable() {
	const probeTimeout = 5 * time.Second
	client := &http.Client{
		Transport: cephClient.Transport,
		Timeout:   probeTimeout,
	}
	for _, name := range clusterOrder {
		st := clusterStates[name]
		urls := st.cfg.MgrPrometheusURLs
		if len(urls) == 0 {
			log.Printf("cluster %s: WARNING no mgr_prometheus_urls configured", name)
			continue
		}
		type result struct {
			url string
			ok  bool
			err error
		}
		results := make(chan result, len(urls))
		for _, u := range urls {
			u := u
			go func() {
				resp, err := client.Get(u)
				if err != nil {
					results <- result{url: u, err: err}
					return
				}
				resp.Body.Close()
				results <- result{url: u, ok: resp.StatusCode < 500}
			}()
		}
		reachable := 0
		for range urls {
			r := <-results
			if r.err != nil {
				log.Printf("cluster %s: mgr endpoint unreachable: %s — %v (probe timeout: %s)", name, r.url, r.err, probeTimeout)
			} else if r.ok {
				log.Printf("cluster %s: mgr endpoint OK: %s", name, r.url)
				reachable++
			} else {
				log.Printf("cluster %s: mgr endpoint error response: %s", name, r.url)
			}
		}
		log.Printf("cluster %s: mgr endpoint probe: %d/%d reachable", name, reachable, len(urls))
	}
}

// loadConfig loads configuration from YAML file
func loadConfig(configFile string) error {
	config = Config{
		Server: ServerConfig{
			Address: "",
			Port:    "8080",
		},
		TLS: TLSConfig{
			Enabled: false,
		},
	}

	if configFile != "" {
		config.TLS.Enabled = true

		data, err := os.ReadFile(configFile)
		if err != nil {
			return fmt.Errorf("failed to read config file %s: %v", configFile, err)
		}

		if err = yaml.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse config file %s: %v", configFile, err)
		}
	}

	// Build cluster state map. No-config mode synthesises a single default cluster.
	clusterOrder = config.Ceph.ClusterOrder
	if len(clusterOrder) == 0 {
		config.Ceph.Clusters = map[string]ClusterConfig{
			"default": {MgrPrometheusURLs: []string{"http://localhost:9283"}},
		}
		clusterOrder = []string{"default"}
	}
	clusterStates = make(map[string]*clusterState, len(clusterOrder))
	for _, name := range clusterOrder {
		cfg := config.Ceph.Clusters[name]
		graphCaches := make(map[string]*metricsCache, len(cfg.PromGraphs))
		for _, g := range cfg.PromGraphs {
			graphCaches[g.ID] = &metricsCache{ttl: 10 * time.Second}
		}
		clusterStates[name] = &clusterState{
			name:            name,
			cfg:             cfg,
			mgrMetrics:      &metricsCache{ttl: 10 * time.Second},
			nodeMetrics:     &metricsCache{ttl: 10 * time.Second},
			promGraphCaches: graphCaches,
		}
	}

	for _, name := range clusterOrder {
		cfg := config.Ceph.Clusters[name]
		log.Printf("cluster %s: mds_hosts=%v extra_hosts=%v", name, cfg.MDSHosts, cfg.ExtraHosts)
	}

	if debug {
		log.Printf("Loaded config: server=%s:%s TLS=%v clusters=%v",
			config.Server.Address, config.Server.Port, config.TLS.Enabled, clusterOrder)
	}

	return nil
}

// writeCertErrorPage writes a self-contained HTML error page explaining that a
// client certificate is needed, linking to tls.cert_help_url and
// tls.access_help_url when they are set. cn is non-empty when a cert was
// presented but the CN is not in the allowed list.
func writeCertErrorPage(w http.ResponseWriter, cn string) {
	link := func(class, url, text string) string {
		if url == "" {
			return ""
		}
		return `<a class="` + class + `" href="` + html.EscapeString(url) + `" target="_blank" rel="noopener">` + text + `</a>`
	}
	certURL, accessURL := config.TLS.CertHelpURL, config.TLS.AccessHelpURL
	var title, body string
	if cn == "" {
		title = "Client Certificate Required"
		body = `<p>This dashboard requires a personal client certificate.<br>
Your browser does not have a valid certificate installed for this site.</p>
` + link("btn", certURL, "Get your certificate &rarr;") + `
<p class="detail">After installing the certificate, reload this page.<br>
You may need to restart your browser for it to appear.</p>`
	} else {
		title = "Certificate Not Authorised"
		body = `<p>Your certificate (<code>CN=` + html.EscapeString(cn) + `</code>) is not on the allow&nbsp;list for this dashboard.</p>
<p>Include this CN when you request access.</p>
` + link("btn", accessURL, "Request access &rarr;")
		if certURL != "" {
			body += `
<p class="detail">Need a certificate first? ` + link("", certURL, html.EscapeString(certURL)) + `</p>`
		}
	}

	page := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + title + ` — Ceph Board</title>
<style>
*{box-sizing:border-box}
body{font-family:system-ui,sans-serif;background:#0f172a;color:#e2e8f0;
     display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:1rem}
.card{background:#1e293b;border:1px solid #334155;border-radius:12px;
      padding:2.5rem 3rem;max-width:500px;width:100%;text-align:center}
.icon{font-size:3rem;margin-bottom:1rem}
h1{color:#f8fafc;font-size:1.4rem;margin:0 0 1rem}
p{color:#94a3b8;line-height:1.65;margin:0 0 1.25rem}
code{background:#0f172a;border-radius:4px;padding:2px 6px;font-size:0.9em;color:#7dd3fc}
a.btn{display:inline-block;background:#3b82f6;color:#fff;text-decoration:none;
      padding:0.65rem 1.6rem;border-radius:7px;font-weight:600;font-size:0.95rem;margin-bottom:1.25rem}
a.btn:hover{background:#2563eb}
.detail{font-size:0.8rem;color:#64748b;margin-top:0.5rem}
.detail a{color:#60a5fa}
</style>
</head>
<body>
<div class="card">
<div class="icon">&#128274;</div>
<h1>` + title + `</h1>
` + body + `
</div>
</body>
</html>`

	status := http.StatusUnauthorized
	if cn != "" {
		status = http.StatusForbidden
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, page)
}

// clientCertAuthMiddleware validates client certificates when TLS is enabled.
// When skip_client_auth is true the server still uses TLS but does not require
// a client certificate — useful for debugging without installing a user cert.
// The TLS listener uses VerifyClientCertIfGiven so the handshake completes even
// without a cert; missing or unauthorised certs are caught here and return a
// user-facing HTML page instead of a raw TLS alert.
func clientCertAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !config.TLS.Enabled || config.TLS.SkipClientAuth {
			next.ServeHTTP(w, r)
			return
		}

		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			writeCertErrorPage(w, "")
			return
		}

		clientCert := r.TLS.PeerCertificates[0]
		clientCN := clientCert.Subject.CommonName

		allowed := false
		for _, allowedCN := range config.TLS.AllowedCNs {
			if clientCN == allowedCN {
				allowed = true
				break
			}
		}

		if !allowed {
			if debug {
				log.Printf("Client certificate CN '%s' not in allowed list: %v", clientCN, config.TLS.AllowedCNs)
			}
			writeCertErrorPage(w, clientCN)
			return
		}

		if debug {
			log.Printf("Client authenticated with CN: %s", clientCN)
		}

		next.ServeHTTP(w, r)
	})
}

// extractCephVersion returns a short version string like "v18.2.7 (reef)" parsed
// from any ceph_*_metadata ceph_version label in the mgr Prometheus body.
// Returns "" when no version label is present.
func extractCephVersion(body []byte) string {
	sub := reCephVersion.FindSubmatch(body)
	if sub == nil {
		return ""
	}
	return "v" + string(sub[1]) + " (" + string(sub[2]) + ")"
}

// fetchMgrMetrics fetches /metrics from a single base URL and returns the body.
// Returns nil body if the response is not 200 OK.
func fetchMgrMetrics(baseURL string) ([]byte, string, error) {
	url := strings.TrimRight(baseURL, "/") + "/metrics"
	resp, err := cephClient.Get(url)
	if err != nil {
		return nil, url, err
	}
	defer resp.Body.Close()
	if verbose || resp.StatusCode >= 400 {
		log.Printf("Ceph GET %s -> %d", url, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, url, nil
	}
	body, err := io.ReadAll(resp.Body)
	return body, url, err
}

type promInstantQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// promGraphRow is one series result from a generic Prometheus graph query.
type promGraphRow struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// promGraphFrontendConfig is the subset of PromGraphConfig sent to the browser via /clusters.
type promGraphFrontendConfig struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Query            string   `json:"query,omitempty"`
	Note             string   `json:"note,omitempty"`
	Warn             *float64 `json:"warn,omitempty"`
	Crit             *float64 `json:"crit,omitempty"`
	ProductionFilter bool     `json:"production_filter,omitempty"`
	FullWidth        bool     `json:"full_width,omitempty"`
	Unit             string   `json:"unit,omitempty"`
	ColorRelative    bool     `json:"color_relative,omitempty"`
	Integer          bool     `json:"integer,omitempty"`
	AlwaysShow       bool     `json:"always_show,omitempty"`
}

// fetchPromGraph runs a PromQL instant query and returns a sorted JSON array of
// {name, value} rows. The series name is built by sorting all non-__name__ label
// keys and joining their values with a space, which deterministically handles
// multi-label cases (e.g. {hostname, fs}) without extra config.
func fetchPromGraph(st *clusterState, cfg PromGraphConfig) ([]byte, error) {
	base := cfg.PrometheusURL
	if base == "" {
		base = st.cfg.PrometheusURL
	}
	if base == "" || cfg.Query == "" {
		return nil, fmt.Errorf("prom-graph %q: prometheus_url and query not configured", cfg.ID)
	}

	reqURL := strings.TrimRight(base, "/") + "/api/v1/query?" + url.Values{"query": {cfg.Query}}.Encode()
	resp, err := cephClient.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("prom-graph %q: request failed: %v", cfg.ID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prom-graph %q: prometheus returned HTTP %d", cfg.ID, resp.StatusCode)
	}

	var parsed promInstantQueryResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("prom-graph %q: invalid JSON response: %v", cfg.ID, err)
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("prom-graph %q: prometheus returned status %q", cfg.ID, parsed.Status)
	}
	if len(parsed.Data.Result) == 0 {
		return []byte("[]"), nil
	}

	rows := make([]promGraphRow, 0, len(parsed.Data.Result))
	for _, r := range parsed.Data.Result {
		if len(r.Value) < 2 {
			continue
		}
		valueStr, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		val, err := strconv.ParseFloat(valueStr, 64)
		if err != nil || val < 0 {
			continue
		}
		// Build a deterministic series name: sort label keys, drop __name__, join values with space.
		keys := make([]string, 0, len(r.Metric))
		for k := range r.Metric {
			if k != "__name__" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = r.Metric[k]
		}
		rows = append(rows, promGraphRow{Name: strings.Join(parts, " "), Value: val})
	}
	if len(rows) == 0 {
		return []byte("[]"), nil
	}

	// Sort by value descending (worst-first).
	sort.Slice(rows, func(i, j int) bool { return rows[i].Value > rows[j].Value })

	// Truncate to max_entries if configured.
	if cfg.MaxEntries > 0 && len(rows) > cfg.MaxEntries {
		rows = rows[:cfg.MaxEntries]
	}

	// PTR-resolve bare IPs (e.g. for node_exporter instance labels like "1.2.3.4:9100").
	if cfg.ResolvePtr {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := range rows {
			name := rows[i].Name
			// Strip port suffix "ip:port" → "ip"
			if idx := strings.LastIndex(name, ":"); idx > 0 {
				if candidate := name[:idx]; net.ParseIP(candidate) != nil {
					rows[i].Name = candidate
					name = candidate
				}
			}
			if net.ParseIP(name) == nil {
				continue
			}
			wg.Add(1)
			go func(i int, ip string) {
				defer wg.Done()
				names, err := net.DefaultResolver.LookupAddr(ctx, ip)
				if err != nil || len(names) == 0 {
					return
				}
				host := strings.TrimSuffix(names[0], ".")
				if dot := strings.Index(host, "."); dot > 0 {
					host = host[:dot]
				}
				mu.Lock()
				rows[i].Name = host
				mu.Unlock()
			}(i, name)
		}
		wg.Wait()
	}

	return json.Marshal(rows)
}

// promGraphHandler serves GET /prom-graph?id=<graphID>&cluster=<name>: runs the
// configured PromQL for the named graph and returns JSON, cached for 10 seconds.
func promGraphHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}
	st, _, err := getClusterState(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	graphID := r.URL.Query().Get("id")
	if graphID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}
	cache, ok := st.promGraphCaches[graphID]
	if !ok {
		http.Error(w, fmt.Sprintf("graph %q not configured", graphID), http.StatusNotFound)
		return
	}
	var gcfg PromGraphConfig
	for _, g := range st.cfg.PromGraphs {
		if g.ID == graphID {
			gcfg = g
			break
		}
	}
	body, err := cache.get(func() ([]byte, error) {
		return fetchPromGraph(st, gcfg)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// isActiveMgr returns true if the Prometheus body contains cluster-level metrics
// that only the active MGR serves (standby MGRs return process metrics only).
func isActiveMgr(body []byte) bool {
	return strings.Contains(string(body), "ceph_health_status")
}

// orderedMgrURLs returns the cluster's URL list with the last known active MGR first.
func orderedMgrURLs(st *clusterState) []string {
	all := st.cfg.MgrPrometheusURLs
	st.activeMgrMu.RLock()
	active := st.activeMgrURL
	st.activeMgrMu.RUnlock()
	if active == "" {
		return all
	}
	reordered := []string{active}
	for _, u := range all {
		if u != active {
			reordered = append(reordered, u)
		}
	}
	return reordered
}

// fetchActiveMgrMetrics fetches /metrics from the active mgr for the given
// cluster, trying all configured URLs in order. It updates activeMgrURL on success.
func fetchActiveMgrMetrics(st *clusterState) ([]byte, error) {
	urls := orderedMgrURLs(st)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no Ceph mgr Prometheus URL configured")
	}
	for _, baseURL := range urls {
		body, url, err := fetchMgrMetrics(baseURL)
		if err != nil {
			if debug {
				log.Printf("Failed to fetch from %s: %v", url, err)
			}
			continue
		}
		if body == nil {
			continue // non-200
		}
		if !isActiveMgr(body) {
			log.Printf("Skipping standby mgr %s (no cluster metrics)", baseURL)
			continue
		}
		st.activeMgrMu.Lock()
		prev := st.activeMgrURL
		st.activeMgrURL = baseURL
		if v := extractCephVersion(body); v != "" {
			st.cephVersion = v
		}
		st.activeMgrMu.Unlock()
		if prev != baseURL {
			if prev == "" {
				log.Printf("Active mgr: %s", baseURL)
			} else {
				log.Printf("Active mgr changed: %s -> %s", prev, baseURL)
			}
		} else if debug {
			log.Printf("Active mgr: %s (unchanged)", baseURL)
		}
		return body, nil
	}
	return nil, fmt.Errorf("failed to fetch Ceph metrics from any of %d configured endpoints (timeout: 30s per URL)", len(urls))
}

// extractOSDMDSHostnames returns the deduplicated, canonicalized hostname list
// from ceph_osd_metadata, ceph_mds_metadata, and ceph_mon_metadata lines in the
// mgr Prometheus body, plus an alias map of short-name → FQDN for every hostname
// that was resolved to a more-qualified form.  The alias map lets the frontend
// canonicalize ceph_osd_metadata entries whose daemons haven't been restarted
// since a hostname change (i.e. still report the old short name).
//
// Two canonicalization passes are applied so that bare short hostnames never reach
// the node_exporter dial step:
//
//  1. IP-peer dedup: when multiple hostnames share the same public_addr IP (e.g.
//     "mds1" and "mds1.site2"), keep only the most-qualified one (most dots, then
//     longest string on a tie).
//
//  2. PTR fallback: if the resulting canonical hostname still has no dots (i.e. it
//     is a bare short name with no qualified peer in the metrics), perform a reverse
//     DNS lookup on the IP from public_addr.  If the PTR record resolves, the FQDN
//     from DNS replaces the short name.  This handles daemons deployed by
//     "ceph fs volume create" which records gethostname() instead of the cephadm
//     host label, but where no other daemon on the same host exposes the full name.
func extractOSDMDSHostnames(body []byte) ([]string, map[string]string) {
	type entry struct {
		hostname string
		ip       string
	}
	var entries []entry
	seenHn := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "ceph_osd_metadata{") &&
			!strings.HasPrefix(line, "ceph_mds_metadata{") &&
			!strings.HasPrefix(line, "ceph_mon_metadata{") {
			continue
		}
		hm := reNodeHostname.FindStringSubmatch(line)
		if hm == nil {
			continue
		}
		h := hm[1]
		if h == "" || seenHn[h] {
			continue
		}
		seenHn[h] = true
		ip := ""
		if im := rePublicAddrIP.FindStringSubmatch(line); im != nil {
			ip = im[1]
		}
		entries = append(entries, entry{hostname: h, ip: ip})
	}

	// Pass 1: IP → canonical hostname (most-qualified among peers sharing an IP).
	ipCanon := map[string]string{}
	for _, e := range entries {
		if e.ip == "" {
			continue
		}
		cur, ok := ipCanon[e.ip]
		if !ok {
			ipCanon[e.ip] = e.hostname
			continue
		}
		cd, hd := strings.Count(cur, "."), strings.Count(e.hostname, ".")
		if hd > cd || (hd == cd && len(e.hostname) > len(cur)) {
			ipCanon[e.ip] = e.hostname
		}
	}

	// Collect canonical (hostname, ip) pairs, deduplicating by canonical name.
	// Track alias map: short → FQDN whenever IP-peer dedup changes the name.
	type canonEntry struct {
		hostname string
		ip       string
	}
	seen := map[string]bool{}
	var canonical []canonEntry
	aliases := map[string]string{}
	for _, e := range entries {
		origH := e.hostname
		h := origH
		if e.ip != "" {
			if c, ok := ipCanon[e.ip]; ok {
				h = c
			}
		}
		if !seen[h] {
			seen[h] = true
			canonical = append(canonical, canonEntry{hostname: h, ip: e.ip})
		}
		if h != origH {
			aliases[origH] = h
		}
	}

	// Pass 2: PTR fallback for bare short hostnames (no dots) that still have an IP.
	result := make([]string, 0, len(canonical))
	for _, ce := range canonical {
		h := ce.hostname
		if !strings.Contains(h, ".") && ce.ip != "" {
			if names, err := net.LookupAddr(ce.ip); err == nil && len(names) > 0 {
				fqdn := strings.TrimSuffix(names[0], ".")
				if fqdn != h {
					aliases[h] = fqdn
				}
				h = fqdn
			}
		}
		result = append(result, h)
	}
	return result, aliases
}

// filterAndLabelNodeMetrics rewrites a node_exporter Prometheus text body to:
//  1. Discard all metric families not in nodeMetricNames.
//  2. Inject instance="hostname" as the first label on every data line.
func filterAndLabelNodeMetrics(body []byte, hostname string) []byte {
	var out strings.Builder
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			// "# HELP name ..." / "# TYPE name ..."
			parts := strings.SplitN(line, " ", 4)
			if len(parts) >= 3 && !nodeMetricNames[parts[2]] {
				continue
			}
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		// Data line — extract metric name to filter
		nameEnd := strings.IndexAny(line, "{ ")
		var name string
		if nameEnd == -1 {
			name = line
		} else {
			name = line[:nameEnd]
		}
		if !nodeMetricNames[name] {
			continue
		}
		// Inject instance label
		labelStart := strings.IndexByte(line, '{')
		spaceIdx := strings.IndexByte(line, ' ')
		if labelStart != -1 && (spaceIdx == -1 || labelStart < spaceIdx) {
			out.WriteString(line[:labelStart+1])
			out.WriteString(`instance="`)
			out.WriteString(hostname)
			out.WriteString(`",`)
			out.WriteString(line[labelStart+1:])
		} else if spaceIdx != -1 {
			out.WriteString(line[:spaceIdx])
			out.WriteString(`{instance="`)
			out.WriteString(hostname)
			out.WriteString(`"}`)
			out.WriteString(line[spaceIdx:])
		} else {
			out.WriteString(line)
		}
		out.WriteByte('\n')
	}
	return []byte(out.String())
}

// fetchAndAggregateNodeMetrics fetches node_exporter /metrics from every OSD
// and MDS host discovered in the cached mgr metrics, aggregates the filtered
// output with per-host instance labels, and returns it as a single body.
func fetchAndAggregateNodeMetrics(st *clusterState) ([]byte, error) {
	mgrBody, err := st.mgrMetrics.get(func() ([]byte, error) {
		return fetchActiveMgrMetrics(st)
	})
	if err != nil {
		return nil, fmt.Errorf("node metrics: cannot discover hosts (mgr unavailable): %v", err)
	}

	hostnames, hostAliases := extractOSDMDSHostnames(mgrBody)

	port := st.cfg.NodeExporterPort
	if port == "" {
		port = "9100"
	}
	suffix := st.cfg.NodeExporterHostSuffix

	// PTR lookup may return the fully-qualified hostname including the domain
	// suffix (e.g. "osd1.site2.example.com" when suffix is ".example.com").
	// Strip it so instance labels use the shorter canonical name ("osd1.site2"),
	// which is what the dashboard tables display and what ceph metadata uses.
	if suffix != "" {
		for i, h := range hostnames {
			if strings.HasSuffix(h, suffix) {
				hostnames[i] = strings.TrimSuffix(h, suffix)
			}
		}
		for k, v := range hostAliases {
			if strings.HasSuffix(v, suffix) {
				hostAliases[k] = strings.TrimSuffix(v, suffix)
			}
		}
	}

	// Merge any explicitly configured MDS hosts into the scrape set.
	// This ensures hosts that have fallen off ceph_mds_metadata (all daemons
	// failed over) are still scraped and shown in the MDS table.
	if len(st.cfg.MDSHosts) > 0 {
		seen := make(map[string]bool, len(hostnames))
		for _, h := range hostnames {
			seen[h] = true
		}
		for _, h := range st.cfg.MDSHosts {
			if !seen[h] {
				hostnames = append(hostnames, h)
				seen[h] = true
			}
		}
	}

	// Merge any explicitly configured extra hosts (e.g. admin/misc nodes that
	// run textfile collectors not discoverable from ceph metadata).
	if len(st.cfg.ExtraHosts) > 0 {
		seen := make(map[string]bool, len(hostnames))
		for _, h := range hostnames {
			seen[h] = true
		}
		for _, h := range st.cfg.ExtraHosts {
			if !seen[h] {
				hostnames = append(hostnames, h)
				seen[h] = true
			}
		}
	}

	// Add MGR/mon hosts from mgr_prometheus_urls into the scrape set.
	// ceph_mon_metadata often still reports bare short hostnames (e.g. "mon1")
	// after a rename, but the mgr URLs contain the correct FQDNs.  Strip the
	// node_exporter_host_suffix from each URL hostname to get the canonical
	// base name (e.g. "mon1.site2.example.com" → "mon1.site2"), then do a
	// forward DNS lookup to match against ceph_mon_metadata public_addr IPs
	// and build short-name aliases so the monitors table can look up node
	// metrics by the old short hostname.
	if len(st.cfg.MgrPrometheusURLs) > 0 {
		// Build IP → []hostnames map from ceph_mon_metadata (all variants, since
		// during a hostname transition both "mon1" and "mon1.site2" may be present
		// for the same IP and we want to alias ALL of them to the canonical).
		ipToMonHostnames := map[string][]string{}
		for _, line := range strings.Split(string(mgrBody), "\n") {
			if !strings.HasPrefix(line, "ceph_mon_metadata{") {
				continue
			}
			hm := reNodeHostname.FindStringSubmatch(line)
			if hm == nil || hm[1] == "" {
				continue
			}
			im := rePublicAddrIP.FindStringSubmatch(line)
			if im == nil || im[1] == "" {
				continue
			}
			ipToMonHostnames[im[1]] = append(ipToMonHostnames[im[1]], hm[1])
		}
		seen := make(map[string]bool, len(hostnames))
		for _, h := range hostnames {
			seen[h] = true
		}
		// Track short mon hostnames that get superseded by a canonical FQDN derived
		// from mgr_prometheus_urls so we can remove them from the scrape list below.
		// Without this, a bare "mon1" (from ceph_mon_metadata) and the correct
		// "mon1.site2" (from the mgr URL) would both be scraped, causing the bare name
		// to be dialled as "mon1.example.com" (wrong) after suffix append.
		replacedByMgr := map[string]bool{}
		for _, rawURL := range st.cfg.MgrPrometheusURLs {
			u, err := url.Parse(rawURL)
			if err != nil {
				continue
			}
			host := u.Hostname() // e.g. "mon1.site2.example.com"
			// Strip suffix to get the base name used as the instance= label.
			base := host
			if suffix != "" && strings.HasSuffix(host, suffix) {
				base = strings.TrimSuffix(host, suffix)
			}
			if !seen[base] {
				hostnames = append(hostnames, base)
				seen[base] = true
			}
			if base == host {
				// No suffix was stripped; hostname is already canonical.
				continue
			}
			// Match any existing (short) hostname that is a proper dot-prefix of
			// base: e.g. "mon1" matches base "mon1.site2" because "mon1.site2" starts
			// with "mon1.".  This handles the case where ceph_mon_metadata reports
			// bare short names but the mgr URLs contain the full FQDN, and the
			// Ceph public_addr IP differs from the management network IP that DNS
			// would resolve the FQDN to (making IP-based matching unreliable).
			for _, existing := range hostnames {
				if existing == base {
					continue
				}
				if strings.HasPrefix(base, existing+".") {
					if _, exists := hostAliases[existing]; !exists {
						hostAliases[existing] = base
						replacedByMgr[existing] = true
					}
				}
			}
			// Also alias any ceph_mon_metadata hostname that is a dot-prefix of base
			// (covers names not yet in the hostnames slice — e.g. if IP-peer dedup
			// already dropped the short name but left no alias for it).
			for _, monHosts := range ipToMonHostnames {
				for _, monHost := range monHosts {
					if monHost == base {
						continue
					}
					if strings.HasPrefix(base, monHost+".") {
						if _, exists := hostAliases[monHost]; !exists {
							hostAliases[monHost] = base
						}
					}
				}
			}
		}
		// Remove any short mon hostnames that the loop above replaced with a
		// canonical FQDN.  They were added by extractOSDMDSHostnames from
		// ceph_mon_metadata short names but are now covered by the FQDN entry.
		if len(replacedByMgr) > 0 {
			filtered := make([]string, 0, len(hostnames))
			for _, h := range hostnames {
				if !replacedByMgr[h] {
					filtered = append(filtered, h)
				}
			}
			hostnames = filtered
		}
	}

	if len(hostnames) == 0 {
		return []byte("# no OSD/MDS hosts discovered from mgr metrics\n"), nil
	}

	log.Printf("cluster %s: node_exporter scrape: %d hosts, port %s, suffix %q",
		st.name, len(hostnames), port, suffix)

	extraHostSet := make(map[string]bool, len(st.cfg.ExtraHosts))
	for _, h := range st.cfg.ExtraHosts {
		extraHostSet[h] = true
	}

	type nodeResult struct {
		hostname string
		target   string
		body     []byte
		err      string
		dur      time.Duration
	}
	ch := make(chan nodeResult, len(hostnames))

	for _, h := range hostnames {
		h := h
		go func() {
			// Don't double-append the suffix if PTR resolution already returned a
			// fully-qualified hostname that ends with it (e.g. PTR returns
			// "osd1.site2.example.com" and suffix is ".example.com").
			target := h
			if suffix != "" && !strings.HasSuffix(h, suffix) {
				target = h + suffix
			}
			addr := target + ":" + port

			t0 := time.Now()
			// Fast TCP reachability check before committing to the full metrics fetch.
			// A 3-second dial timeout gives quick failure for down hosts without the
			// full 30-second wait that a metrics payload can take on a loaded host.
			dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
			conn, dialErr := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
			dialCancel()
			if dialErr != nil {
				ch <- nodeResult{hostname: h, target: target, err: "unreachable: " + dialErr.Error(), dur: time.Since(t0)}
				return
			}
			conn.Close()

			url := "http://" + addr + "/metrics"
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			resp, fetchErr := cephClient.Do(req)
			if fetchErr != nil {
				ch <- nodeResult{hostname: h, target: target, err: fetchErr.Error(), dur: time.Since(t0)}
				return
			}
			defer resp.Body.Close()
			b, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				ch <- nodeResult{hostname: h, target: target, err: readErr.Error(), dur: time.Since(t0)}
				return
			}
			ch <- nodeResult{hostname: h, target: target, body: b, dur: time.Since(t0)}
		}()
	}

	var out strings.Builder
	var nOK, nFail int
	var failMsgs []string
	var unreachable []string
	for range hostnames {
		r := <-ch
		if r.body == nil {
			nFail++
			failMsgs = append(failMsgs, r.target+": "+r.err)
			unreachable = append(unreachable, r.hostname)
			if extraHostSet[r.hostname] {
				log.Printf("cluster %s: extra_host %s: FAIL (%s): %s", st.name, r.target, r.dur.Round(time.Millisecond), r.err)
			}
			continue
		}
		nOK++
		if extraHostSet[r.hostname] {
			nInc := bytes.Count(r.body, []byte("ceph_mds_rank_incarnation{"))
			log.Printf("cluster %s: extra_host %s: ok (%s): %d ceph_mds_rank_incarnation lines", st.name, r.target, r.dur.Round(time.Millisecond), nInc)
		}
		out.Write(filterAndLabelNodeMetrics(r.body, r.hostname))
	}

	if len(unreachable) > 0 {
		out.WriteString("\n# HELP go_ceph_node_unreachable Node unreachable via node_exporter (synthetic, emitted by go-ceph-board)\n")
		out.WriteString("# TYPE go_ceph_node_unreachable gauge\n")
		for _, h := range unreachable {
			fmt.Fprintf(&out, "go_ceph_node_unreachable{instance=%q} 1\n", h)
		}
	}

	if len(hostAliases) > 0 {
		out.WriteString("\n# HELP go_ceph_host_canonical Canonical FQDN resolved from a bare short hostname via IP-peer dedup or PTR lookup (synthetic, emitted by go-ceph-board)\n")
		out.WriteString("# TYPE go_ceph_host_canonical gauge\n")
		for short, fqdn := range hostAliases {
			fmt.Fprintf(&out, "go_ceph_host_canonical{instance=%q,canonical=%q} 1\n", short, fqdn)
		}
	}

	if len(st.cfg.MDSHosts) > 0 {
		out.WriteString("\n# HELP go_ceph_configured_mds_host Host declared as MDS host in config (synthetic, emitted by go-ceph-board)\n")
		out.WriteString("# TYPE go_ceph_configured_mds_host gauge\n")
		for _, h := range st.cfg.MDSHosts {
			fmt.Fprintf(&out, "go_ceph_configured_mds_host{instance=%q} 1\n", h)
		}
	}

	log.Printf("cluster %s: node_exporter scrape done: %d ok, %d failed", st.name, nOK, nFail)
	for _, msg := range failMsgs {
		log.Printf("cluster %s: node_exporter fail: %s", st.name, msg)
	}

	aggregated := []byte(out.String())
	nInc := bytes.Count(aggregated, []byte("ceph_mds_rank_incarnation{"))
	rankTracker.updateFromNodeMetrics(st.name, aggregated)
	nAssigned := rankTracker.assignedCount(st.name)
	log.Printf("cluster %s: rank tracking: %d ceph_mds_rank_incarnation lines in node metrics, %d rank slots recorded", st.name, nInc, nAssigned)
	return aggregated, nil
}

// nodeMetricsHandler serves GET /node-metrics: aggregated node_exporter data
// from all OSD and MDS hosts, filtered and labeled with instance="hostname".
// Accepts optional ?cluster= query param; defaults to the first configured cluster.
func nodeMetricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	st, _, err := getClusterState(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := st.nodeMetrics.get(func() ([]byte, error) {
		return fetchAndAggregateNodeMetrics(st)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// cephMetricsHandler serves GET /ceph-metrics from a 10-second server-side cache.
// Accepts optional ?cluster= query param; defaults to the first configured cluster.
func cephMetricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	st, _, err := getClusterState(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := st.mgrMetrics.get(func() ([]byte, error) {
		return fetchActiveMgrMetrics(st)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	w.Write(body)

	// Append synthetic metric so the frontend knows which host runs the active mgr.
	st.activeMgrMu.RLock()
	mgrURL := st.activeMgrURL
	st.activeMgrMu.RUnlock()
	if mgrURL != "" {
		if u, err := url.Parse(mgrURL); err == nil && u.Hostname() != "" {
			fmt.Fprintf(w,
				"\n# HELP go_ceph_active_mgr_host Active ceph-mgr hostname (synthetic, emitted by go-ceph-board)\n"+
					"# TYPE go_ceph_active_mgr_host gauge\n"+
					"go_ceph_active_mgr_host{hostname=%q} 1\n",
				u.Hostname())
		}
	}

}

// clustersHandler serves GET /clusters: returns cluster list, graph configs, and thresholds.
func clustersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}
	defaultCluster := ""
	if len(clusterOrder) > 0 {
		defaultCluster = clusterOrder[0]
	}
	versions := make(map[string]string, len(clusterOrder))
	productionCephFSVolumes := make(map[string][]string, len(clusterOrder))
	promGraphs := make(map[string][]promGraphFrontendConfig, len(clusterOrder))
	for _, name := range clusterOrder {
		st := clusterStates[name]
		st.activeMgrMu.RLock()
		versions[name] = st.cephVersion
		st.activeMgrMu.RUnlock()
		if len(st.cfg.ProductionCephFSVolumes) > 0 {
			productionCephFSVolumes[name] = st.cfg.ProductionCephFSVolumes
		}
		graphs := make([]promGraphFrontendConfig, len(st.cfg.PromGraphs))
		for i, g := range st.cfg.PromGraphs {
			graphs[i] = promGraphFrontendConfig{
				ID:               g.ID,
				Title:            g.Title,
				Description:      g.Description,
				Query:            g.Query,
				Note:             g.Note,
				Warn:             g.Warn,
				Crit:             g.Crit,
				ProductionFilter: g.ProductionFilter,
				FullWidth:        g.FullWidth,
				Unit:             g.Unit,
				ColorRelative:    g.ColorRelative,
				Integer:          g.Integer,
				AlwaysShow:       g.AlwaysShow,
			}
		}
		promGraphs[name] = graphs
	}
	thresh := config.Thresholds
	if thresh.UsagePct == nil {
		thresh.UsagePct = &ThresholdPair{Warn: 70, Crit: 85}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"clusters":                   clusterOrder,
		"default":                    defaultCluster,
		"versions":                   versions,
		"external_links":             config.ExternalLinks,
		"sites":                      config.Sites,
		"host_link":                  config.HostLink,
		"document_header":            config.DocumentHeader,
		"production_ceph_fs_volumes": productionCephFSVolumes,
		"prom_graphs":                promGraphs,
		"thresholds":                 thresh,
	})
}

// healthHandler serves GET /health for HAProxy health checks.
// It returns 200 OK without requiring a client certificate.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK\n"))
}

// faviconHandler serves favicon.ico from the embedded static folder
func faviconHandler(w http.ResponseWriter, r *http.Request) {
	file, err := staticDir.Open("static/favicon.ico")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "image/x-icon")
	io.Copy(w, file)
}

func main() {
	var (
		versionFlag = flag.Bool("version", false, "show build time and version number")
		configFile  = flag.String("config", "", "path to YAML configuration file")
	)
	flag.BoolVar(&debug, "debug", false, "log debug output, defaults to false")
	flag.BoolVar(&verbose, "verbose", false, "log verbose output, defaults to false")
	flag.Parse()

	if *versionFlag {
		fmt.Println("go-ceph-board", buildversion, " Build time:", buildtime, "UTC")
		os.Exit(0)
	}

	if err := loadConfig(*configFile); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	if err := buildCephClient(); err != nil {
		log.Fatalf("Failed to build Ceph HTTP client: %v", err)
	}
	log.Printf("Configured clusters: %v", clusterOrder)
	checkMgrURLsReachable()
	rankFile := "rank_assignments.json"
	if *configFile != "" {
		rankFile = filepath.Join(filepath.Dir(*configFile), "rank_assignments.json")
	}
	rankTracker.load(rankFile)
	warmupCaches()

	authMiddleware := clientCertAuthMiddleware

	http.Handle("/", authMiddleware(http.HandlerFunc(dashboardHandler)))
	http.Handle("/static/", authMiddleware(http.FileServer(http.FS(staticDir))))
	http.Handle("/favicon.ico", authMiddleware(http.HandlerFunc(faviconHandler)))
	http.Handle("/ceph-metrics", authMiddleware(http.HandlerFunc(cephMetricsHandler)))
	http.Handle("/node-metrics", authMiddleware(http.HandlerFunc(nodeMetricsHandler)))
	http.Handle("/prom-graph", authMiddleware(http.HandlerFunc(promGraphHandler)))
	http.Handle("/clusters", authMiddleware(http.HandlerFunc(clustersHandler)))
	http.Handle("/rank-assignments", authMiddleware(http.HandlerFunc(rankAssignmentsHandler)))

	address := config.Server.Address
	port := config.Server.Port
	listenAddr := address + ":" + port
	if address == "" {
		listenAddr = "0.0.0.0:" + port
	}

	protocol := "http"
	if config.TLS.Enabled {
		protocol = "https"
	}
	fmt.Printf("go-ceph-board version %s (built %s UTC) starting on %s://%s\n",
		buildversion, buildtime, protocol, listenAddr)
	fmt.Println("All static assets are embedded. You can run this binary standalone.")

	if config.TLS.Enabled {
		fmt.Printf("TLS client certificate authentication enabled, CA: %s, allowed CNs: %v\n",
			config.TLS.CAFile, config.TLS.AllowedCNs)

		var err error
		certManager, err = NewCertificateManager(config.TLS.CertFile, config.TLS.KeyFile, config.TLS.CAFile)
		if err != nil {
			log.Fatalf("Failed to initialize certificate manager: %v", err)
		}

		// VerifyClientCertIfGiven lets the TLS handshake complete without a cert.
		// The middleware catches the no-cert / wrong-CN cases and returns a helpful
		// HTML page instead of a raw TLS alert that the browser can't explain.
		// A cert that IS presented but fails CA verification still aborts the handshake.
		clientAuth := tls.VerifyClientCertIfGiven
		if config.TLS.SkipClientAuth {
			clientAuth = tls.NoClientCert
			log.Printf("WARNING: skip_client_auth=true — TLS server cert active but no client certificate required")
		}
		tlsConfig := &tls.Config{
			ClientAuth:     clientAuth,
			ClientCAs:      certManager.GetCACertPool(),
			GetCertificate: certManager.GetCertificate,
		}

		server := &http.Server{
			Addr:      listenAddr,
			TLSConfig: tlsConfig,
		}

		fmt.Printf("Certificate monitoring enabled — certificates reload automatically on file changes\n")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		serverErr := make(chan error, 1)
		go func() {
			serverErr <- server.ListenAndServeTLS("", "")
		}()

		// Optional TLS-only (no client cert) port for HAProxy health checks.
		var hcServer *http.Server
		if config.TLS.HealthCheckPort != "" {
			hcAddr := config.Server.Address + ":" + config.TLS.HealthCheckPort
			if config.Server.Address == "" {
				hcAddr = "0.0.0.0:" + config.TLS.HealthCheckPort
			}
			hcMux := http.NewServeMux()
			hcMux.HandleFunc("/health", healthHandler)
			hcServer = &http.Server{
				Addr:    hcAddr,
				Handler: hcMux,
			}
			log.Printf("Health-check listener (plain HTTP) starting on http://%s/health", hcAddr)
			go func() {
				if err := hcServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("Health-check server error: %v", err)
				}
			}()
		}

		select {
		case err := <-serverErr:
			if err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server error: %v", err)
			}
		case sig := <-sigChan:
			log.Printf("Received signal %s, shutting down gracefully...", sig)
			shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
			defer shutdownCancel()

			if hcServer != nil {
				if err := hcServer.Shutdown(shutdownCtx); err != nil {
					log.Printf("Health-check server shutdown error: %v", err)
				}
			}
			if err := server.Shutdown(shutdownCtx); err != nil {
				log.Printf("Server shutdown error: %v", err)
			}
			if err := certManager.Close(); err != nil {
				log.Printf("Certificate manager cleanup error: %v", err)
			} else {
				log.Printf("Certificate manager stopped")
			}
		}
	} else {
		log.Fatal(http.ListenAndServe(listenAddr, nil))
	}
}
