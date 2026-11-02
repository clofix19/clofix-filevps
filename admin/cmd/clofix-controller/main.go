package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/clofix/filevps/admin/internal/pki"
	"github.com/clofix/filevps/admin/internal/server"
	"github.com/clofix/filevps/admin/internal/store"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	if os.Getenv("CLOFIX_ADMIN_EMAIL") == "" || os.Getenv("CLOFIX_ADMIN_PASSWORD") == "" {
		log.Fatal("CLOFIX_ADMIN_EMAIL and CLOFIX_ADMIN_PASSWORD must be set (password min 12 chars)")
	}
	dataDir := env("CLOFIX_DATA_DIR", "/var/lib/clofix-controller")
	ca, err := pki.LoadOrCreate(dataDir + "/pki")
	if err != nil {
		log.Fatal("pki: ", err)
	}
	names := strings.Split(env("CLOFIX_TLS_NAMES", "localhost,127.0.0.1,admin"), ",")
	certPEM, keyPEM, err := ca.ServerCert(names)
	if err != nil {
		log.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		log.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)

	st := newStore()
	srv := server.New(st, ca, "", env("CLOFIX_UI_DIR", "web/admin-ui"))
	if err := srv.EnsureBootstrapAdmin(); err != nil {
		log.Fatal(err)
	}
	go srv.RunApplicationScheduler(context.Background())
	go srv.RunLoadBalancerScheduler(context.Background())
	go srv.RunBillingMeter(context.Background())
	go srv.RunHealthScheduler(context.Background())
	go srv.RunLicenseScheduler(context.Background())
	go srv.RunDatabaseScheduler(context.Background())
	go srv.ServeDNS(env("CLOFIX_DNS_LISTEN", ":53"))
	go srv.ServeHTTPNames(env("CLOFIX_HTTP_LISTEN", ":80"))
	go srv.ServeHTTPSNames(env("CLOFIX_HTTPS_LISTEN", ":443"))
	srv.LogDir = env("CLOFIX_LOG_DIR", "/worker-logs/instances")

	go func() {
		ws := &http.Server{
			Addr: env("CLOFIX_WORKER_LISTEN", ":8443"), Handler: srv.WorkerHandler(),
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13,
				ClientCAs: pool, ClientAuth: tls.VerifyClientCertIfGiven, // enroll has no cert yet; heartbeat enforces one
			},
		}
		log.Printf("worker API (mTLS) on %s", ws.Addr)
		log.Fatal(ws.ListenAndServeTLS("", ""))
	}()

	addr := env("CLOFIX_ADMIN_LISTEN", ":8080")
	log.Printf("admin API + UI on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv.AdminHandler()))
}

// newStore returns the in-memory store; Postgres is selected with -tags postgres (see store_postgres.go).
func newStore() store.Store { return storeFactory() }
