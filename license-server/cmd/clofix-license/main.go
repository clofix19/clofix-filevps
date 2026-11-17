package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/clofix/license-server/internal/license"
)

func main() {
	dir := os.Getenv("CLOFIX_LICENSE_DATA_DIR")
	if dir == "" {
		dir = "/data"
	}
	if os.Getenv("CLOFIX_LICENSE_ADMIN_TOKEN") != "" || os.Getenv("LICENSE_ADMIN_TOKEN") != "" {
		log.Fatal("LICENSE_ADMIN_TOKEN is removed; set CLOFIX_LICENSE_ADMIN_USER and CLOFIX_LICENSE_ADMIN_PASSWORD")
	}
	service, e := license.NewWithDatabase(
		dir,
		os.Getenv("CLOFIX_LICENSE_ADMIN_USER"),
		os.Getenv("CLOFIX_LICENSE_ADMIN_PASSWORD"),
		os.Getenv("CLOFIX_LICENSE_DATABASE_URL"),
	)
	if e != nil {
		log.Fatal(e)
	}
	address := os.Getenv("CLOFIX_LICENSE_LISTEN")
	if address == "" {
		address = ":8090"
	}
	backend := "file"
	if os.Getenv("CLOFIX_LICENSE_DATABASE_URL") != "" {
		backend = "postgres"
	}
	log.Printf("license server on %s (store=%s)", address, backend)
	server := &http.Server{Addr: address, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
