package main

import (
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
	adminTok := os.Getenv("CLOFIX_ADMIN_TOKEN")
	if len(adminTok) < 16 {
		log.Fatal("CLOFIX_ADMIN_TOKEN must be set (min 16 chars)")
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
	srv := server.New(st, ca, adminTok, env("CLOFIX_UI_DIR", "web/admin-ui"))
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
