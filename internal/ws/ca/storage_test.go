package ca

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStorage_RootRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.json")
	s := NewFileStorage(path)

	_, _, err := s.LoadRootCA()
	if err == nil {
		t.Fatal("expected error loading root from empty storage")
	}

	certPEM := []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
	keyPEM := []byte("-----BEGIN EC PRIVATE KEY-----\nMIIB\n-----END EC PRIVATE KEY-----\n")
	if err := s.StoreRootCA(certPEM, keyPEM); err != nil {
		t.Fatalf("StoreRootCA: %v", err)
	}

	// A fresh storage instance over the same file must see the root.
	s2 := NewFileStorage(path)
	gotCert, gotKey, err := s2.LoadRootCA()
	if err != nil {
		t.Fatalf("LoadRootCA after reopen: %v", err)
	}
	if string(gotCert) != string(certPEM) || string(gotKey) != string(keyPEM) {
		t.Error("root material mismatch after reopen")
	}
}

func TestFileStorage_RevocationPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.json")
	s := NewFileStorage(path)

	if err := s.AddRevoked("serial-1"); err != nil {
		t.Fatalf("AddRevoked: %v", err)
	}

	// Fresh instance over the same file.
	s2 := NewFileStorage(path)
	revoked, err := s2.IsRevoked("serial-1")
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !revoked {
		t.Error("expected serial-1 to be revoked after reopen")
	}

	revoked, err = s2.IsRevoked("serial-2")
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if revoked {
		t.Error("serial-2 should not be revoked")
	}

	// Duplicate revocations are idempotent.
	if err := s2.AddRevoked("serial-1"); err != nil {
		t.Fatalf("AddRevoked duplicate: %v", err)
	}
}

func TestFileStorage_MissingFileIsEmpty(t *testing.T) {
	s := NewFileStorage(filepath.Join(t.TempDir(), "does-not-exist", "ca.json"))
	revoked, err := s.IsRevoked("x")
	if err != nil {
		t.Fatalf("IsRevoked on missing file: %v", err)
	}
	if revoked {
		t.Error("expected not revoked on missing file")
	}
}

func TestFileStorage_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewFileStorage(path)
	if _, _, err := s.LoadRootCA(); err == nil {
		t.Error("expected error for corrupt file")
	}
}
