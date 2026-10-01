package main

import (
	"fmt"
	"net/http"
	"strings"
)

const dashboardHTML = htmlPage +
	htmlJSUtils +
	htmlJSMetrics +
	htmlJSMonitors +
	htmlJSMDS +
	htmlJSHosts +
	htmlJSMain

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/cluster/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, strings.ReplaceAll(dashboardHTML, "{{buildversion}}", buildversion))
}
