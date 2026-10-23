package admin

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"time"
)

// CertFacts are the facts of a secret's public certificate. There is no field
// for the certificate body, and the private key is never read.
type CertFacts struct {
	NotAfter time.Time
	Serial   string
	SANs     []string
}

type dataSource struct {
	InlineString string `json:"inline_string"`
	InlineBytes  string `json:"inline_bytes"` // base64 in Envoy's JSON
}

func (d dataSource) pem() []byte {
	switch {
	case d.InlineString != "":
		return []byte(d.InlineString)
	case d.InlineBytes != "":
		if b, err := base64.StdEncoding.DecodeString(d.InlineBytes); err == nil {
			return b
		}
	}
	return nil
}

// certFacts reads the first certificate of a tls_certificate chain or a
// validation_context trusted CA. Anything unreadable yields nil, including a
// first PEM block that is not a certificate.
func certFacts(secret map[string]json.RawMessage) *CertFacts {
	var src dataSource
	switch {
	case secret["tls_certificate"] != nil:
		var tc struct {
			Chain dataSource `json:"certificate_chain"`
		}
		if json.Unmarshal(secret["tls_certificate"], &tc) != nil {
			return nil
		}
		src = tc.Chain
	case secret["validation_context"] != nil:
		var vc struct {
			CA dataSource `json:"trusted_ca"`
		}
		if json.Unmarshal(secret["validation_context"], &vc) != nil {
			return nil
		}
		src = vc.CA
	}
	block, _ := pem.Decode(src.pem())
	if block == nil || block.Type != "CERTIFICATE" {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	f := &CertFacts{Serial: cert.SerialNumber.Text(16), NotAfter: cert.NotAfter.UTC()}
	f.SANs = append(f.SANs, cert.DNSNames...)
	for _, u := range cert.URIs {
		f.SANs = append(f.SANs, u.String())
	}
	for _, ip := range cert.IPAddresses {
		f.SANs = append(f.SANs, ip.String())
	}
	f.SANs = append(f.SANs, cert.EmailAddresses...)
	return f
}
