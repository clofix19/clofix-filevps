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
