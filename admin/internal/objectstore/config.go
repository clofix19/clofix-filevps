// Package objectstore stores S3-compatible connection credentials encrypted at rest.
package objectstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Config struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

type PublicConfig struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Endpoint   string `json:"endpoint"`
	Bucket     string `json:"bucket"`
	Region     string `json:"region,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
	Configured bool   `json:"configured"`
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) key() ([]byte, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	p := filepath.Join(s.dir, "s3-master.key")
	b, err := os.ReadFile(p)
	if err == nil {
		if len(b) != 32 {
			return nil, errors.New("invalid S3 encryption key")
		}
		return b, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return b, nil
}

func valid(c Config) error {
	if c.Provider != "" && c.Provider != "aws" && c.Provider != "gcs" && c.Provider != "azure_s3" && c.Provider != "custom" && c.Provider != "clofix" {
		return errors.New("unsupported S3 provider")
	}
	u, err := url.ParseRequestURI(c.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("endpoint must be an http(s) S3 endpoint without credentials or query parameters")
	}
	if c.Bucket == "" || len(c.Bucket) > 255 || strings.ContainsAny(c.Bucket, "/\\\x00\r\n") {
		return errors.New("invalid bucket name")
	}
	if c.AccessKey == "" || len(c.AccessKey) > 256 || strings.ContainsAny(c.AccessKey, "\x00\r\n") {
		return errors.New("invalid access key")
	}
	if c.SecretKey == "" || len(c.SecretKey) > 1024 || strings.ContainsAny(c.SecretKey, "\x00\r\n") {
		return errors.New("invalid secret key")
	}
	if len(c.Prefix) > 1024 || strings.ContainsAny(c.Prefix, "\\\x00\r\n") {
		return errors.New("invalid S3 prefix")
	}
	for _, part := range strings.Split(strings.Trim(c.Prefix, "/"), "/") {
		if part == "." || part == ".." {
			return errors.New("S3 prefix cannot contain dot path segments")
		}
	}
	return nil
}

func (s *Store) Save(c Config) error {
	if err := valid(c); err != nil {
		return err
	}
	key, err := s.key()
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	body := append([]byte("CFS3"), nonce...)
	body = aead.Seal(body, nonce, plain, []byte("clofix-s3-config-v1"))
	tmp := filepath.Join(s.dir, "s3-config.enc.tmp")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, "s3-config.enc")); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) Load() (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(s.dir, "s3-config.enc"))
	if err != nil {
		return c, err
	}
	if len(b) < 4 || string(b[:4]) != "CFS3" {
		return c, errors.New("invalid encrypted S3 configuration")
	}
	key, err := s.key()
	if err != nil {
		return c, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return c, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return c, err
	}
	if len(b) < 4+aead.NonceSize() {
		return c, errors.New("truncated encrypted S3 configuration")
	}
	nonce := b[4 : 4+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, b[4+aead.NonceSize():], []byte("clofix-s3-config-v1"))
	if err != nil {
		return c, fmt.Errorf("decrypt S3 configuration: %w", err)
	}
	if err := json.Unmarshal(plain, &c); err != nil {
		return c, err
	}
	return c, nil
}

func (s *Store) Public() PublicConfig {
	c, err := s.Load()
	if err != nil {
		return PublicConfig{}
	}
	return PublicConfig{Endpoint: c.Endpoint, Bucket: c.Bucket, Region: c.Region, Prefix: c.Prefix, Provider: c.Provider, Configured: true}
}

func public(c Config) PublicConfig {
	return PublicConfig{ID: c.ID, Name: c.Name, Provider: c.Provider, Endpoint: c.Endpoint, Bucket: c.Bucket, Region: c.Region, Prefix: c.Prefix, Configured: true}
}

func (s *Store) ListConnections() ([]Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listConnections()
}

func (s *Store) listConnections() ([]Config, error) {
	path := filepath.Join(s.dir, "s3-connections.enc")
	b, err := os.ReadFile(path)
	if err == nil {
		plain, e := s.decrypt(b, "CFS4", []byte("clofix-s3-connections-v1"))
		if e != nil {
			return nil, e
		}
		var configs []Config
		if e = json.Unmarshal(plain, &configs); e != nil {
			return nil, e
		}
		return configs, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	legacy, e := s.Load()
	if e != nil {
		return []Config{}, nil
	}
	legacy.ID, legacy.Name = "s3-default", "Default S3 connection"
	return []Config{legacy}, nil
}

func (s *Store) GetConnection(id string) (Config, error) {
	items, err := s.ListConnections()
	if err != nil {
		return Config{}, err
	}
	for _, c := range items {
		if c.ID == id {
			return c, nil
		}
	}
	return Config{}, os.ErrNotExist
}

func (s *Store) SaveConnection(c Config) error {
	if err := valid(c); err != nil {
		return err
	}
	if c.ID == "" || len(c.ID) > 64 {
		return errors.New("invalid S3 connection id")
	}
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 80 {
		return errors.New("connection name must be 1–80 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.listConnections()
	if err != nil {
		return err
	}
	found := false
	for i := range items {
		if items[i].ID == c.ID {
			items[i] = c
			found = true
			break
		}
	}
	if !found {
		items = append(items, c)
	}
	plain, err := json.Marshal(items)
	if err != nil {
		return err
	}
	body, err := s.encrypt(plain, "CFS4", []byte("clofix-s3-connections-v1"))
	if err != nil {
		return err
	}
	return s.writeAtomic("s3-connections.enc", body)
}

func (s *Store) DeleteConnection(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.listConnections()
	if err != nil {
		return err
	}
	out := make([]Config, 0, len(items))
	found := false
	for _, c := range items {
		if c.ID == id {
			found = true
			continue
		}
		out = append(out, c)
	}
	if !found {
		return os.ErrNotExist
	}
	plain, err := json.Marshal(out)
	if err != nil {
		return err
	}
	body, err := s.encrypt(plain, "CFS4", []byte("clofix-s3-connections-v1"))
	if err != nil {
		return err
	}
	return s.writeAtomic("s3-connections.enc", body)
}

func (s *Store) PublicConnections() ([]PublicConfig, error) {
	configs, err := s.ListConnections()
	if err != nil {
		return nil, err
	}
	out := make([]PublicConfig, 0, len(configs))
	for _, c := range configs {
		out = append(out, public(c))
	}
	return out, nil
}

func (s *Store) encrypt(plain []byte, magic string, aad []byte) ([]byte, error) {
	key, err := s.key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	body := append([]byte(magic), nonce...)
	return aead.Seal(body, nonce, plain, aad), nil
}

func (s *Store) decrypt(body []byte, magic string, aad []byte) ([]byte, error) {
	if len(body) < 4 || string(body[:4]) != magic {
		return nil, errors.New("invalid encrypted S3 connection store")
	}
	key, err := s.key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(body) < 4+aead.NonceSize() {
		return nil, errors.New("truncated encrypted S3 connection store")
	}
	nonce := body[4 : 4+aead.NonceSize()]
	return aead.Open(nil, nonce, body[4+aead.NonceSize():], aad)
}

func (s *Store) writeAtomic(name string, b []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, name+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, name)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
