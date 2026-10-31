package database

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

func BackupFormat(engine, filename string) (string, error) {
	if filename == "" || path.Base(filename) != filename || strings.ContainsAny(filename, "\\\x00\r\n") {
		return "", fmt.Errorf("invalid backup filename")
	}
	for _, format := range []string{"sql.gz", "archive.gz", "sql", "dump", "archive"} {
		if strings.HasSuffix(strings.ToLower(filename), "."+format) && compatibleFormat(engine, format) {
			return format, nil
		}
	}
	return "", fmt.Errorf("unsupported backup format for engine")
}
func compatibleFormat(engine, format string) bool {
	switch engine {
	case "mysql":
		return format == "sql" || format == "sql.gz"
	case "postgresql":
		return format == "sql" || format == "sql.gz" || format == "dump"
	case "mongodb":
		return format == "archive" || format == "archive.gz"
	}
	return false
}

// ValidateContent fully consumes the stream, verifies gzip checksums and expansion
// limits, and checks native format signatures. Native tools still parse into an
// isolated staging database before cutover; a signature is not semantic validation.
func ValidateContent(engine, format string, source io.Reader, expandedLimit int64) error {
	if expandedLimit < 1 || !compatibleFormat(engine, format) {
		return fmt.Errorf("incompatible backup format or limit")
	}
	if strings.HasSuffix(format, ".gz") {
		compressed, e := gzip.NewReader(source)
		if e != nil {
			return fmt.Errorf("invalid gzip backup")
		}
		defer compressed.Close()
		source = compressed
	}
	reader := bufio.NewReader(io.LimitReader(source, expandedLimit+1))
	var count int64
	switch {
	case strings.HasPrefix(format, "sql"):
		for {
			value, n, e := reader.ReadRune()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			count += int64(n)
			if count > expandedLimit {
				return fmt.Errorf("expanded backup exceeds storage limit")
			}
			if value == 0 || value == utf8.RuneError && n == 1 {
				return fmt.Errorf("SQL backup must contain UTF-8 text without NUL bytes")
			}
		}
	case format == "dump":
		var signature [5]byte
		if _, e := io.ReadFull(reader, signature[:]); e != nil || string(signature[:]) != "PGDMP" {
			return fmt.Errorf("invalid PostgreSQL custom dump signature")
		}
		count = 5
	case strings.HasPrefix(format, "archive"):
		var signature [4]byte
		if _, e := io.ReadFull(reader, signature[:]); e != nil || binary.LittleEndian.Uint32(signature[:]) != 0x8199e26d {
			return fmt.Errorf("invalid MongoDB archive signature")
		}
		count = 4
	}
	if !strings.HasPrefix(format, "sql") {
		n, e := io.Copy(io.Discard, reader)
		if e != nil {
			return e
		}
		count += n
	}
	if count == 0 {
		return fmt.Errorf("backup is empty")
	}
	if count > expandedLimit {
		return fmt.Errorf("expanded backup exceeds storage limit")
	}
	return nil
}
