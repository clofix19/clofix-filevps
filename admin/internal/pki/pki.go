// Package pki is the controller's private CA: it signs worker CSRs and its own server cert.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}

// LoadOrCreate loads the CA from dir or creates a new one (key file mode 0600).
func LoadOrCreate(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	cp, kp := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if cb, err := os.ReadFile(cp); err == nil {
		kb, err := os.ReadFile(kp)
		if err != nil {
			return nil, err
		}
		cb2, _ := pem.Decode(cb)
		kb2, _ := pem.Decode(kb)
		if cb2 == nil || kb2 == nil {
			return nil, errors.New("invalid CA pem")
		}
		cert, err := x509.ParseCertificate(cb2.Bytes)
		if err != nil {
			return nil, err
		}
		key, err := x509.ParseECPrivateKey(kb2.Bytes)
		if err != nil {
			return nil, err
		}
		return &CA{cert, key, cb}, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "CloFix FileVPS CA", Organization: []string{"CloFix Infotech"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	kd, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(cp, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600); err != nil {
		return nil, err
	}
	return &CA{cert, key, certPEM}, nil
}

// SignWorkerCSR issues a client cert. The identity comes from the controller (workerID), never from the CSR.
func (c *CA) SignWorkerCSR(csrPEM []byte, workerID string, ttl time.Duration) ([]byte, error) {
	blk, _ := pem.Decode(csrPEM)
	if blk == nil {
		return nil, errors.New("invalid CSR pem")
	}
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		return nil, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: workerID, Organization: []string{"clofix-worker"}},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(ttl),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.Cert, csr.PublicKey, c.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// ServerCert issues the controller's TLS server certificate for the given DNS names / IPs.
func (c *CA) ServerCert(names []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "clofix-controller"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.Cert, &key.PublicKey, c.Key)
	if err != nil {
		return nil, nil, err
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), nil
}
