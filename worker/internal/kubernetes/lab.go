package kubernetes

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
)

// Internal listener stays outside CloFix published FileVPS ranges so host
// nftables cannot drop it. The admin console proxies this on /cks-lab/.
const labHTTPPort = "19080"

func labPublicURL() string {
	if u := strings.TrimSpace(os.Getenv("CLOFIX_KUBERNETES_LAB_PUBLIC_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://127.0.0.1:8080/cks-lab"
}

func (m *Manager) ensureLabHTTP() string {
	m.labOnce.Do(func() {
		port := strings.TrimSpace(os.Getenv("CLOFIX_KUBERNETES_LAB_HTTP_PORT"))
		if port == "" {
			port = labHTTPPort
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Write([]byte("ok"))
		})
		mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"service": "nginx",
				"cluster": "cks-lab",
				"status":  "ok",
				"message": "CKS lab nginx. This is not a production kube-apiserver.",
			})
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(labNginxWelcome))
		})
		ln, err := net.Listen("tcp", "0.0.0.0:"+port)
		if err != nil {
			return
		}
		m.labURL = labPublicURL()
		go http.Serve(ln, labCORS(mux))
	})
	return m.labURL
}

func labCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const labNginxWelcome = `<!DOCTYPE html>
<html>
<head>
<title>Welcome to nginx!</title>
<style>
html { color-scheme: light dark; }
body { width: 35em; margin: 0 auto; font-family: Tahoma, Verdana, Arial, sans-serif; }
</style>
</head>
<body>
<h1>Welcome to nginx!</h1>
<p>If you see this page, the CKS lab nginx workload is reachable.</p>
<p>JSON API: <a href="api">api</a> · health: <a href="healthz">healthz</a></p>
<p><em>Thank you for using nginx.</em></p>
</body>
</html>
`
