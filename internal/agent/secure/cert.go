package secure

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
)

// CertificateAuthority manages the Hub self-signed CA and client cert issuance.
type CertificateAuthority struct {
	mu       sync.Mutex
	caCert   *x509.Certificate
	caKey    *ecdsa.PrivateKey
	caPEM    []byte
	caKeyPEM []byte
	tokens   map[string]time.Time // token -> expires_at
}

// NewCertificateAuthority creates or initializes a self-signed root CA.
func NewCertificateAuthority() (*CertificateAuthority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"back-EZ Internal Security"},
			CommonName:   "back-EZ Root CA",
		},
		NotBefore:             time.Now().Add(-10 * time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, err
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	return &CertificateAuthority{
		caCert:   caCert,
		caKey:    key,
		caPEM:    caPEM,
		caKeyPEM: caKeyPEM,
		tokens:   make(map[string]time.Time),
	}, nil
}

// CAPEM returns the Root CA certificate in PEM format.
func (ca *CertificateAuthority) CAPEM() []byte {
	return ca.caPEM
}

// GenerateServerCert issues a TLS server certificate for the Hub.
func (ca *CertificateAuthority) GenerateServerCert(dnsNames []string, ips []net.IP) (tls.Certificate, []byte, []byte, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"back-EZ Hub"},
			CommonName:   "back-ez-hub",
		},
		NotBefore:   time.Now().Add(-10 * time.Minute),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    dnsNames,
		IPAddresses: ips,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, ca.caCert, &key.PublicKey, ca.caKey)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	return tlsCert, certPEM, keyPEM, nil
}

// GenerateClientCert issues a TLS client certificate for an Agent bound to clientID.
func (ca *CertificateAuthority) GenerateClientCert(clientID string) (tls.Certificate, []byte, []byte, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"back-EZ Agent"},
			CommonName:   clientID, // bind client_id to CommonName
		},
		NotBefore:   time.Now().Add(-10 * time.Minute),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour), // 1 year (SPEC §3.3)
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, ca.caCert, &key.PublicKey, ca.caKey)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	return tlsCert, certPEM, keyPEM, nil
}

// CreateEnrollToken generates a single-use token for agent enrollment.
func (ca *CertificateAuthority) CreateEnrollToken(ttl time.Duration) string {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	token := fmt.Sprintf("%x", bytes)
	ca.tokens[token] = time.Now().Add(ttl)
	return token
}

// ConsumeEnrollToken verifies and invalidates an enrollment token.
func (ca *CertificateAuthority) ConsumeEnrollToken(token string) bool {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	expires, exists := ca.tokens[token]
	if !exists {
		return false
	}
	delete(ca.tokens, token) // one-time use immediately invalidated
	return time.Now().Before(expires)
}

// NeedsRotation checks if certificate is within 30 days of expiration (SPEC §3.3).
func NeedsRotation(cert *x509.Certificate) bool {
	if cert == nil {
		return true
	}
	cutoff := time.Now().Add(30 * 24 * time.Hour)
	return cert.NotAfter.Before(cutoff)
}

// ServerTLSConfig creates mTLS config for Hub HTTPS/WSS endpoints.
func (ca *CertificateAuthority) ServerTLSConfig(serverCert tls.Certificate) *tls.Config {
	certPool := x509.NewCertPool()
	certPool.AddCert(ca.caCert)

	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool,
		MinVersion:   tls.VersionTLS12,
	}
}

// ClientTLSConfig creates mTLS config for Agent to connect to Hub.
func (ca *CertificateAuthority) ClientTLSConfig(clientCert tls.Certificate) *tls.Config {
	certPool := x509.NewCertPool()
	certPool.AddCert(ca.caCert)

	return &tls.Config{
		Certificates:       []tls.Certificate{clientCert},
		RootCAs:            certPool,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false, // RED LINE: Must NEVER skip verification
	}
}

// ExtractClientIDFromCert retrieves the CommonName from peer certificate.
func ExtractClientIDFromCert(conn *tls.ConnectionState) (string, error) {
	if conn == nil || len(conn.PeerCertificates) == 0 {
		return "", errors.New("no client certificates provided")
	}
	cn := conn.PeerCertificates[0].Subject.CommonName
	if cn == "" {
		return "", errors.New("empty common name in client certificate")
	}
	return cn, nil
}
