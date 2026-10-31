package main

import (
	"github.com/clofix/license-server/internal/license"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	dir := os.Getenv("CLOFIX_LICENSE_DATA_DIR")
	if dir == "" {
		dir = "/data"
	}
	service, e := license.New(dir, os.Getenv("CLOFIX_LICENSE_ADMIN_TOKEN"))
	if e != nil {
		log.Fatal(e)
	}
	address := os.Getenv("CLOFIX_LICENSE_LISTEN")
	if address == "" {
		address = ":8090"
	}
	log.Printf("license server on %s", address)
	server := &http.Server{Addr: address, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
