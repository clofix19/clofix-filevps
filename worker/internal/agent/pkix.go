package agent

import "crypto/x509/pkix"

// The CSR subject is informational only: the controller sets the real identity in the issued certificate.
func pkixName(host string) pkix.Name { return pkix.Name{CommonName: host} }
