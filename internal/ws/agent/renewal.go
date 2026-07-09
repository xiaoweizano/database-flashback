package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"time"

	"github.com/a-shan/mysql-pitr/internal/ws"
)

// CheckCertExpiry returns the number of whole days until the first
// certificate in the TLS config expires. Returns -1 if the config has no
// certificates or the leaf cannot be parsed. Returns 0 if the certificate
// has already expired.
func CheckCertExpiry(cfg *tls.Config) int {
	if cfg == nil || len(cfg.Certificates) == 0 {
		return -1
	}

	cert := cfg.Certificates[0]
	if len(cert.Certificate) == 0 {
		return -1
	}

	// Use the cached Leaf if available, otherwise parse.
	var leaf *x509.Certificate
	if cert.Leaf != nil {
		leaf = cert.Leaf
	} else {
		parsed, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return -1
		}
		leaf = parsed
		// Cache for next call.
		cert.Leaf = leaf
		cfg.Certificates[0] = cert
	}

	remaining := time.Until(leaf.NotAfter)
	days := int(remaining.Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// GenerateCSR creates a PKCS#10 certificate request using a fresh ECDSA
// P-256 key pair. It returns the PEM-encoded CSR and the private key. The
// caller should keep the key to pair with the signed certificate returned by
// the platform.
func GenerateCSR(agentID string) (csrPEM []byte, key *ecdsa.PrivateKey, err error) {
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CSR key: %w", err)
	}

	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: agentID,
		},
	}

	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create CSR: %w", err)
	}

	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	return csrPEM, key, nil
}

// AutoRenew generates a new ECDSA P-256 key and CSR, sends it to the
// platform via the Client for signing, and updates the provided tls.Config
// with the renewed certificate. The connection is not dropped — the existing
// WebSocket continues to function; the new certificate will be used on the
// next reconnection handshake.
func AutoRenew(ctx context.Context, client *Client, tlsCfg *tls.Config, agentID string) (*tls.Certificate, error) {
	csrPEM, key, err := GenerateCSR(agentID)
	if err != nil {
		return nil, err
	}

	cmd := ws.Command{
		Cmd:  fmt.Sprintf("cert-renewal-%d", time.Now().UnixNano()),
		Type: ws.CmdCertRenewal,
		Params: map[string]interface{}{
			"csr": string(csrPEM),
		},
	}

	resp, err := client.SendCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("send CSR to platform: %w", err)
	}
	if resp.Status == ws.StatusError {
		return nil, fmt.Errorf("platform rejected CSR: %s", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response result type")
	}
	certPEMStr, ok := result["certificate"].(string)
	if !ok || certPEMStr == "" {
		return nil, fmt.Errorf("missing 'certificate' in platform response")
	}

	// Marshal the private key back to PEM.
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal EC key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	newCert, err := tls.X509KeyPair([]byte(certPEMStr), keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse renewed certificate: %w", err)
	}

	// Swap the certificate in the TLS config. The existing WebSocket
	// connection continues unaffected; subsequent TLS handshakes (e.g. after
	// reconnect) will use the new certificate.
	tlsCfg.Certificates = []tls.Certificate{newCert}

	log.Printf("certificate renewed for agent %s (expires %s)",
		agentID, newCert.Leaf.NotAfter.Format(time.RFC3339))

	return &newCert, nil
}

// StartAutoRenew launches a background goroutine that checks certificate
// expiry every 24 hours. When fewer than 7 days remain, it triggers an
// automatic renewal via AutoRenew. The goroutine exits when ctx is
// cancelled.
func StartAutoRenew(ctx context.Context, client *Client, tlsCfg *tls.Config, agentID string) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		// Check once immediately.
		days := CheckCertExpiry(tlsCfg)
		if days >= 0 && days < 7 {
			log.Printf("cert for agent %s expires in %d days, renewing now", agentID, days)
			if _, err := AutoRenew(ctx, client, tlsCfg, agentID); err != nil {
				log.Printf("initial auto-renew failed: %v", err)
			}
		}

		for {
			select {
			case <-ticker.C:
				days := CheckCertExpiry(tlsCfg)
				if days >= 0 && days < 7 {
					log.Printf("cert for agent %s expires in %d days, renewing", agentID, days)
					if _, err := AutoRenew(ctx, client, tlsCfg, agentID); err != nil {
						log.Printf("auto-renew failed: %v", err)
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

