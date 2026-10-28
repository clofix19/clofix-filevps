//go:build !postgres

package main

import (
	"log"

	"github.com/clofix/filevps/admin/internal/store"
)

func storeFactory() store.Store {
	log.Println("WARNING: using in-memory store (state lost on restart); build with -tags postgres for PostgreSQL")
	return store.NewMemory()
}
