package ca

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// fileStoreData is the on-disk JSON shape of FileStorage.
type fileStoreData struct {
	CACert  string   `json:"caCert"`
	CAKey   string   `json:"caKey"`
	Revoked []string `json:"revoked,omitempty"`
}

// FileStorage is a CertStorage backed by a single JSON file. It survives
// server restarts and uses atomic writes (temp file + rename).
type FileStorage struct {
	mu   sync.Mutex
	path string
}

// NewFileStorage creates a FileStorage rooted at path. The file is created
// lazily on the first write.
func NewFileStorage(path string) *FileStorage {
	return &FileStorage{path: path}
}

// LoadRootCA reads the stored root certificate and key.
func (s *FileStorage) LoadRootCA() (certPEM, keyPEM []byte, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.read()
	if err != nil {
		return nil, nil, err
	}
	if data.CACert == "" {
		return nil, nil, fmt.Errorf("file storage: no root CA stored at %s", s.path)
	}
	return []byte(data.CACert), []byte(data.CAKey), nil
}

// StoreRootCA persists the root certificate and key.
func (s *FileStorage) StoreRootCA(certPEM, keyPEM []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.read()
	if err != nil {
		// First write: start with an empty document.
		data = &fileStoreData{}
	}
	data.CACert = string(certPEM)
	data.CAKey = string(keyPEM)
	return s.write(data)
}

// IsRevoked reports whether the given certificate serial is revoked.
func (s *FileStorage) IsRevoked(serial string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.read()
	if err != nil {
		return false, nil
	}
	for _, r := range data.Revoked {
		if r == serial {
			return true, nil
		}
	}
	return false, nil
}

// AddRevoked records a certificate serial as revoked.
func (s *FileStorage) AddRevoked(serial string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.read()
	if err != nil {
		data = &fileStoreData{}
	}
	for _, r := range data.Revoked {
		if r == serial {
			return nil // already revoked
		}
	}
	data.Revoked = append(data.Revoked, serial)
	sort.Strings(data.Revoked)
	return s.write(data)
}

// read loads the store file. Must be called with the lock held. A missing
// file is not an error.
func (s *FileStorage) read() (*fileStoreData, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &fileStoreData{}, nil
		}
		return nil, fmt.Errorf("file storage: read %s: %w", s.path, err)
	}
	var data fileStoreData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("file storage: parse %s: %w", s.path, err)
	}
	return &data, nil
}

// write atomically persists the store. Must be called with the lock held.
func (s *FileStorage) write(data *fileStoreData) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("file storage: create dir: %w", err)
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("file storage: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return fmt.Errorf("file storage: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("file storage: rename: %w", err)
	}
	return nil
}
