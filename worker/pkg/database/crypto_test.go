package database

import (
	"bytes"
	"io"
	"testing"
)

func TestEncryptedBackupRoundTripAndTampering(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	data := bytes.Repeat([]byte("database-record\n"), 100000)
	var encrypted bytes.Buffer
	if _, e := Encrypt(&encrypted, bytes.NewReader(data), key, "dbb-test", int64(len(data))); e != nil {
		t.Fatal(e)
	}
	var plain bytes.Buffer
	if _, e := Decrypt(&plain, bytes.NewReader(encrypted.Bytes()), key, "dbb-test", int64(len(data))); e != nil || !bytes.Equal(plain.Bytes(), data) {
		t.Fatal("round trip", e)
	}
	for _, bad := range [][]byte{encrypted.Bytes()[:encrypted.Len()-1], append(append([]byte{}, encrypted.Bytes()...), 0), func() []byte { b := append([]byte{}, encrypted.Bytes()...); b[30] ^= 1; return b }()} {
		if _, e := Decrypt(&bytes.Buffer{}, bytes.NewReader(bad), key, "dbb-test", int64(len(data))); e == nil {
			t.Fatal("corruption accepted")
		}
	}
	if _, e := Decrypt(&bytes.Buffer{}, bytes.NewReader(encrypted.Bytes()), key, "another-backup", int64(len(data))); e == nil {
		t.Fatal("cross-object substitution accepted")
	}
}
func TestSchemaRejectsSQLInjection(t *testing.T) {
	for _, table := range []string{"a; DROP TABLE b", "x\"", "../x"} {
		if _, e := TableDDL("postgresql", table, []Column{{Name: "id", Type: "INTEGER"}}); e == nil {
			t.Fatal("unsafe name accepted")
		}
	}
	if _, e := TableDDL("mysql", "items", []Column{{Name: "id", Type: "INTEGER); DROP DATABASE x--"}}); e == nil {
		t.Fatal("unsafe type accepted")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestEncryptRejectsShortWrites(t *testing.T) {
	if _, e := Encrypt(shortWriter{}, bytes.NewReader([]byte("row")), bytes.Repeat([]byte{1}, 32), "backup", 3); e == nil {
		t.Fatal("short write accepted")
	}
}
func TestRangeReaderAuthenticatesRecords(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	for _, size := range []int{0, 1, ChunkBytes, ChunkBytes + 19, ChunkBytes * 3} {
		data := bytes.Repeat([]byte{42}, size)
		var encrypted bytes.Buffer
		if _, e := Encrypt(&encrypted, bytes.NewReader(data), key, "backup", int64(size)); e != nil {
			t.Fatal(e)
		}
		reader, e := OpenReader(bytes.NewReader(encrypted.Bytes()), int64(encrypted.Len()), int64(size), key, "backup")
		if e != nil {
			t.Fatalf("size %d: %v", size, e)
		}
		got, e := io.ReadAll(reader)
		if e != nil || !bytes.Equal(got, data) {
			t.Fatal("range round trip", e)
		}
		if size > ChunkBytes {
			if _, e = reader.Seek(ChunkBytes-10, io.SeekStart); e != nil {
				t.Fatal(e)
			}
			got = make([]byte, 29)
			if _, e = io.ReadFull(reader, got); e != nil || !bytes.Equal(got, data[ChunkBytes-10:ChunkBytes+19]) {
				t.Fatal("cross-record range", e)
			}
		}
		bad := append([]byte{}, encrypted.Bytes()...)
		bad[len(bad)-1] ^= 1
		if _, e = OpenReader(bytes.NewReader(bad), int64(len(bad)), int64(size), key, "backup"); e == nil {
			t.Fatal("invalid footer accepted")
		}
		if _, e = OpenReader(bytes.NewReader(encrypted.Bytes()), int64(encrypted.Len()), int64(size+1), key, "backup"); e == nil {
			t.Fatal("untrusted size accepted")
		}
	}
}
