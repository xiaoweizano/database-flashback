package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const (
	// RootValidity is the validity period for the root CA certificate.
	RootValidity = 10 * 365 * 24 * time.Hour
	// CertValidity is the validity period for signed client certificates.
	CertValidity = 90 * 24 * time.Hour
)

// CertStorage provides persistence for the CA's root certificate and
// revocation list. All methods must be safe for concurrent use.
type CertStorage interface {
	// LoadRootCA returns the PEM-encoded root certificate and private key.
	LoadRootCA() (certPEM, keyPEM []byte, err error)
	// StoreRootCA persists the PEM-encoded root certificate and private key.
	StoreRootCA(certPEM, keyPEM []byte) error
	// IsRevoked reports whether the given certificate serial has been revoked.
	IsRevoked(serial string) (bool, error)
	// AddRevoked records a certificate serial as revoked.
	AddRevoked(serial string) error
}

// CA is an internal certificate authority that generates root certificates,
// signs CSRs for agent client certificates, manages revocation, and provides
// a TLS configuration for the platform server.
type CA struct {
	storage CertStorage

	// Cached root key material (set after GenerateRoot or LoadRoot).
	rootCert *x509.Certificate
	rootKey  *ecdsa.PrivateKey

	// Serial number counter for signed certificates.
	serialMu sync.Mutex
	serial   int64

	// In-memory revocation cache.
	revoked   map[string]bool
	revokedMu sync.RWMutex
}

// NewCA creates a CA backed by the given storage. Call GenerateRoot to
// initialise the root if one does not already exist in storage.
func NewCA(storage CertStorage) *CA {
	return &CA{
		storage: storage,
		revoked: make(map[string]bool),
	}
}

// GenerateRoot creates an ECDSA P-256 self-signed root CA certificate with
// a 10-year validity and stores it via the configured CertStorage. It is
// safe to call multiple times — subsequent calls are no-ops if a root
// already exists in storage.
func (ca *CA) GenerateRoot() (*tls.Certificate, error) {
	// Check if root already exists in storage.
	if certPEM, keyPEM, err := ca.storage.LoadRootCA(); err == nil && certPEM != nil {
		return ca.loadRoot(certPEM, keyPEM)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ca: generate root key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("ca: generate root serial: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Platform Internal CA",
			Organization: []string{"MySQL PITR Platform"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(RootValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("ca: create root cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("ca: marshal root key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := ca.storage.StoreRootCA(certPEM, keyPEM); err != nil {
		return nil, fmt.Errorf("ca: store root: %w", err)
	}

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("ca: parse root cert: %w", err)
	}
	ca.rootCert = parsed
	ca.rootKey = key

	// Load revocation list.
	ca.loadRevoked()

	return &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        parsed,
	}, nil
}

// loadRoot parses cached PEM into the CA's in-memory state.
func (ca *CA) loadRoot(certPEM, keyPEM []byte) (*tls.Certificate, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("ca: decode root cert PEM")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("ca: decode root key PEM")
	}

	parsed, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse root cert: %w", err)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse root key: %w", err)
	}

	ca.rootCert = parsed
	ca.rootKey = key
	ca.loadRevoked()

	return &tls.Certificate{
		Certificate: [][]byte{certBlock.Bytes},
		PrivateKey:  key,
		Leaf:        parsed,
	}, nil
}

// loadRevoked is a placeholder for loading revocation state from storage
// on startup. The current implementation populates the cache on demand
// via IsRevoked.
func (ca *CA) loadRevoked() {
}

// SignCSR validates a PEM-encoded PKCS#10 CSR and signs it with the CA root
// key, returning a PEM-encoded signed certificate with 90-day validity. The
// certificate's CommonName is set to agentID (overriding any CN in the CSR).
func (ca *CA) SignCSR(csrPEM []byte, agentID string) ([]byte, error) {
	if ca.rootCert == nil || ca.rootKey == nil {
		return nil, fmt.Errorf("ca: root not initialised; call GenerateRoot first")
	}

	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("ca: invalid CSR PEM")
	}

	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse CSR: %w", err)
	}

	// Validate the CSR signature.
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("ca: CSR signature validation failed: %w", err)
	}

	// Use the CSR's public key (must be ECDSA).
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("ca: CSR public key must be ECDSA")
	}

	ca.serialMu.Lock()
	ca.serial++
	serial := big.NewInt(ca.serial)
	ca.serialMu.Unlock()

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   agentID,
			Organization: []string{"MySQL PITR Agent"},
		},
		NotBefore: now.Add(-1 * time.Hour),
		NotAfter:  now.Add(CertValidity),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
		},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca.rootCert, pub, ca.rootKey)
	if err != nil {
		return nil, fmt.Errorf("ca: sign CSR: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// Revoke marks a certificate serial number as revoked. Once revoked, the
// serial is persisted via CertStorage and future IsRevoked calls return true.
func (ca *CA) Revoke(serial string) error {
	ca.revokedMu.Lock()
	ca.revoked[serial] = true
	ca.revokedMu.Unlock()

	if err := ca.storage.AddRevoked(serial); err != nil {
		return fmt.Errorf("ca: persist revocation: %w", err)
	}
	return nil
}

// IsRevoked returns true if the given certificate serial has been revoked.
func (ca *CA) IsRevoked(serial string) bool {
	// Check in-memory cache first.
	ca.revokedMu.RLock()
	revoked, ok := ca.revoked[serial]
	ca.revokedMu.RUnlock()
	if ok {
		return revoked
	}

	// Fall through to storage.
	stored, err := ca.storage.IsRevoked(serial)
	if err != nil {
		return false
	}

	// Cache the result.
	ca.revokedMu.Lock()
	ca.revoked[serial] = stored
	ca.revokedMu.Unlock()
	return stored
}

// ServerTLSConfig returns a *tls.Config configured for mTLS with
// RequireAndVerifyClientCert using the CA's root certificate as the client
// CA pool. The config does not include a server certificate — the caller
// must add one via Certificates.
func (ca *CA) ServerTLSConfig() *tls.Config {
	pool := x509.NewCertPool()
	if ca.rootCert != nil {
		pool.AddCert(ca.rootCert)
	}

	return &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  pool,
		MinVersion: tls.VersionTLS12,
	}
}

// ---------------------------------------------------------------------------
// In-memory storage implementation
// ---------------------------------------------------------------------------

// MemStorage is an in-memory CertStorage suitable for testing and
// single-instance deployments.
type MemStorage struct {
	mu      sync.RWMutex
	caCert  []byte
	caKey   []byte
	revoked map[string]bool
}

// NewMemStorage returns an initialised MemStorage.
func NewMemStorage() *MemStorage {
	return &MemStorage{
		revoked: make(map[string]bool),
	}
}

func (m *MemStorage) LoadRootCA() (certPEM, keyPEM []byte, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.caCert == nil {
		return nil, nil, fmt.Errorf("mem: no root CA stored")
	}
	return append([]byte{}, m.caCert...), append([]byte{}, m.caKey...), nil
}

func (m *MemStorage) StoreRootCA(certPEM, keyPEM []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caCert = append([]byte{}, certPEM...)
	m.caKey = append([]byte{}, keyPEM...)
	return nil
}

func (m *MemStorage) IsRevoked(serial string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.revoked[serial], nil
}

func (m *MemStorage) AddRevoked(serial string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[serial] = true
	return nil
}
