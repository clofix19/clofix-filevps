package database

import (
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
)

// Reader supports authenticated HTTP range downloads without decrypting a whole
// backup into a temporary plaintext file. The caller obtains plainSize from trusted
// backup metadata. Each requested record and the end marker are authenticated.
// Reader is a per-download cursor; use ReadAt for concurrent reads.
type Reader struct {
	source   io.ReaderAt
	cipher   cipher.AEAD
	prefix   [8]byte
	object   string
	size     int64
	position int64
}

func OpenReader(source io.ReaderAt, cipherSize, plainSize int64, key []byte, object string) (*Reader, error) {
	if plainSize < 0 {
		return nil, fmt.Errorf("invalid backup size")
	}
	c, e := aead(key)
	if e != nil {
		return nil, e
	}
	records := plainSize / ChunkBytes
	if plainSize%ChunkBytes != 0 {
		records++
	}
	// Avoid overflowing the record nonce and arithmetic on untrusted metadata.
	if records > int64(^uint32(0)) || plainSize > int64(^uint64(0)>>1)-(records+1)*20-16 {
		return nil, fmt.Errorf("backup record limit")
	}
	expected := int64(16) + plainSize + (records+1)*20
	if cipherSize != expected {
		return nil, fmt.Errorf("encrypted backup size mismatch")
	}
	var header [16]byte
	if _, e = source.ReadAt(header[:], 0); e != nil || string(header[:8]) != string(magic) {
		return nil, fmt.Errorf("invalid encrypted backup header")
	}
	r := &Reader{source: source, cipher: c, object: object, size: plainSize}
	copy(r.prefix[:], header[8:])
	if _, e = r.record(records, 0); e != nil {
		return nil, e
	}
	if records > 0 { // Authenticate the final data record, including its claimed size.
		n := int(plainSize - (records-1)*ChunkBytes)
		if _, e = r.record(records-1, n); e != nil {
			return nil, e
		}
	}
	return r, nil
}
func (r *Reader) record(index int64, n int) ([]byte, error) {
	offset := int64(16) + index*(ChunkBytes+20)
	if n == 0 {
		offset = int64(16) + r.size + index*20
	}
	raw := make([]byte, 4+n+r.cipher.Overhead())
	if _, e := r.source.ReadAt(raw, offset); e != nil {
		return nil, fmt.Errorf("truncated encrypted backup")
	}
	if binary.BigEndian.Uint32(raw[:4]) != uint32(n) {
		return nil, fmt.Errorf("invalid backup record size")
	}
	nonce := make([]byte, 12)
	copy(nonce, r.prefix[:])
	binary.BigEndian.PutUint32(nonce[8:], uint32(index))
	plain, e := r.cipher.Open(nil, nonce, raw[4:], append([]byte(r.object), nonce...))
	if e != nil {
		return nil, fmt.Errorf("backup integrity check failed")
	}
	return plain, nil
}
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative backup offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= r.size {
		return 0, io.EOF
	}
	written := 0
	for written < len(p) && off < r.size {
		index := off / ChunkBytes
		size := int64(ChunkBytes)
		if remaining := r.size - index*ChunkBytes; remaining < size {
			size = remaining
		}
		plain, e := r.record(index, int(size))
		if e != nil {
			return written, e
		}
		n := copy(p[written:], plain[off%ChunkBytes:])
		written += n
		off += int64(n)
	}
	if written < len(p) {
		return written, io.EOF
	}
	return written, nil
}
func (r *Reader) Read(p []byte) (int, error) {
	n, e := r.ReadAt(p, r.position)
	r.position += int64(n)
	return n, e
}
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	base := int64(0)
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = r.position
	case io.SeekEnd:
		base = r.size
	default:
		return r.position, fmt.Errorf("invalid seek origin")
	}
	if offset < -base || offset > int64(^uint64(0)>>1)-base {
		return r.position, fmt.Errorf("invalid seek offset")
	}
	r.position = base + offset
	return r.position, nil
}
