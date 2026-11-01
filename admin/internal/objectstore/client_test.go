package objectstore

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPutReaderStreamsKnownPayload(t *testing.T) {
	content := strings.Repeat("encrypted-database-backup", 10000)
	digest := sha256.Sum256([]byte(content))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/bucket/prefix/database-backups/test.enc" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(digest[:]) || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("request was not signed with the streamed payload digest")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != content {
			t.Error("streamed body mismatch", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	config := Config{Endpoint: server.URL, Bucket: "bucket", Prefix: "prefix", AccessKey: "access", SecretKey: "secret", Region: "us-east-1"}
	if err := PutReader(config, "database-backups/test.enc", "application/octet-stream", strings.NewReader(content), int64(len(content)), hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
}

func TestListAllCopyAndHead(t *testing.T) {
	objects := map[string]bool{"prefix/css/app.css": true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
			w.Header().Set("Content-Type", "application/xml")
			io.WriteString(w, `<?xml version="1.0"?><ListBucketResult><Contents><Key>prefix/css/app.css</Key><Size>12</Size><LastModified>2026-01-02T03:04:05Z</LastModified></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`)
		case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/prefix/css/app.css"):
			if objects["prefix/css/app.css"] {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			if r.Header.Get("X-Amz-Copy-Source") != "/bucket/prefix/css/app.css" {
				t.Errorf("copy source %s", r.Header.Get("X-Amz-Copy-Source"))
			}
			if !strings.HasSuffix(r.URL.Path, "/prefix/assets/app.css") {
				t.Errorf("copy dest %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	config := Config{Endpoint: server.URL, Bucket: "bucket", Prefix: "prefix", AccessKey: "access", SecretKey: "secret", Region: "us-east-1"}
	items, err := ListAll(config, "css")
	if err != nil || len(items) != 1 || items[0].Key != "css/app.css" || items[0].Folder {
		t.Fatalf("list all: %+v %v", items, err)
	}
	found, err := Head(config, "css/app.css")
	if err != nil || !found {
		t.Fatal("head source", found, err)
	}
	missing, err := Head(config, "assets/app.css")
	if err != nil || missing {
		t.Fatal("head dest", missing, err)
	}
	if err := Copy(config, "css/app.css", "assets/app.css"); err != nil {
		t.Fatal(err)
	}
}
