package database

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

// Encrypted streams authenticate ordering, object identity, and an explicit end marker.
// A missing/truncated final record is an integrity failure, including empty backups.
const ChunkBytes = 1 << 20

var magic = []byte("CFDBAK01")

func aead(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("backup key must be 32 bytes")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func Encrypt(dst io.Writer, src io.Reader, key []byte, object string, limit int64) (int64, error) {
	c, e := aead(key)
	if e != nil {
		return 0, e
	}
	prefix := make([]byte, 8)
	if _, e = rand.Read(prefix); e != nil {
		return 0, e
	}
	header := append(append([]byte{}, magic...), prefix...)
	if _, e = writeAll(dst, header); e != nil {
		return 0, e
	}
	buffer := make([]byte, ChunkBytes)
	var total int64
	var counter uint32
	for {
		n, readErr := io.ReadFull(src, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return total, readErr
		}
		if total+int64(n) > limit {
			return total, fmt.Errorf("backup exceeds reserved storage limit")
		}
		nonce := make([]byte, 12)
		copy(nonce, prefix)
		binary.BigEndian.PutUint32(nonce[8:], counter)
		aad := append([]byte(object), nonce...)
		sealed := c.Seal(nil, nonce, buffer[:n], aad)
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(n))
		if _, e = writeAll(dst, size[:]); e != nil {
			return total, e
		}
		if _, e = writeAll(dst, sealed); e != nil {
			return total, e
		}
		total += int64(n)
		if n == 0 {
			return total, nil
		}
		if counter == ^uint32(0) {
			return total, fmt.Errorf("backup record limit")
		}
		counter++
		if readErr != nil {
			src = &emptyReader{}
		}
	}
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }
func Decrypt(dst io.Writer, src io.Reader, key []byte, object string, limit int64) (int64, error) {
	c, e := aead(key)
	if e != nil {
		return 0, e
	}
	header := make([]byte, 16)
	if _, e = io.ReadFull(src, header); e != nil || string(header[:8]) != string(magic) {
		return 0, fmt.Errorf("invalid encrypted backup header")
	}
	var total int64
	var counter uint32
	for {
		var size [4]byte
		if _, e = io.ReadFull(src, size[:]); e != nil {
			return total, fmt.Errorf("truncated backup")
		}
		n := binary.BigEndian.Uint32(size[:])
		if n > ChunkBytes || total+int64(n) > limit {
			return total, fmt.Errorf("backup size limit")
		}
		sealed := make([]byte, int(n)+c.Overhead())
		if _, e = io.ReadFull(src, sealed); e != nil {
			return total, fmt.Errorf("truncated backup")
		}
		nonce := make([]byte, 12)
		copy(nonce, header[8:])
		binary.BigEndian.PutUint32(nonce[8:], counter)
		plain, e := c.Open(nil, nonce, sealed, append([]byte(object), nonce...))
		if e != nil {
			return total, fmt.Errorf("backup integrity check failed")
		}
		if n == 0 {
			var extra [1]byte
			if n, e := src.Read(extra[:]); n != 0 || e != io.EOF {
				return total, fmt.Errorf("unexpected trailing backup data")
			}
			return total, nil
		}
		written, e := dst.Write(plain)
		total += int64(written)
		if e != nil {
			return total, e
		}
		if written != len(plain) {
			return total, io.ErrShortWrite
		}
		if counter == ^uint32(0) {
			return total, fmt.Errorf("backup record limit")
		}
		counter++
	}
}

func writeAll(dst io.Writer, value []byte) (int, error) {
	n, err := dst.Write(value)
	if err == nil && n != len(value) {
		err = io.ErrShortWrite
	}
	return n, err
}
