//go:build postgres

package main

import (
	"database/sql"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver; run `go mod tidy` when building with -tags postgres

	"github.com/clofix/filevps/admin/internal/store"
)

func storeFactory() store.Store {
	url := os.Getenv("CLOFIX_DB_URL")
	if url == "" {
		log.Fatal("CLOFIX_DB_URL is required in postgres builds")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		log.Fatal("database: ", err)
	}
	return store.NewPostgres(db)
}
