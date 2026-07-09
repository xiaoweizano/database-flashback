package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

const (
	// SaltLen is the length of the random salt used in PBKDF2 key derivation.
	SaltLen = 16
	// NonceLen is the length of the AES-GCM nonce (12 bytes for best performance).
	NonceLen = 12
	// KeyLen is the AES-256 key length.
	KeyLen = 32
	// PBKDF2Iterations is the number of PBKDF2 iterations.
	PBKDF2Iterations = 100000
)

// Encrypt encrypts plaintext using AES-256-GCM with a key derived from the
// passphrase via PBKDF2 (SHA-256, 100,000 iterations).
//
// The output format is: hex(salt || nonce || ciphertext)
// where salt is 16 random bytes, nonce is 12 random bytes, and ciphertext
// is the AES-GCM output (which includes the authentication tag).
func Encrypt(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, SaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("config: generate salt: %w", err)
	}

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("config: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("config: new GCM: %w", err)
	}

	nonce := make([]byte, NonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("config: generate nonce: %w", err)
	}

	// Seal appends the encrypted data and auth tag to the nonce prefix.
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Prepend salt.
	out := make([]byte, 0, SaltLen+len(nonce)+len(ciphertext))
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	// Encode to hex for safe file storage.
	encoded := make([]byte, hex.EncodedLen(len(out)))
	hex.Encode(encoded, out)

	return encoded, nil
}

// Decrypt decrypts a payload produced by Encrypt using the same passphrase.
func Decrypt(ciphertext []byte, passphrase string) ([]byte, error) {
	// Decode from hex.
	raw := make([]byte, hex.DecodedLen(len(ciphertext)))
	n, err := hex.Decode(raw, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("config: hex decode: %w", err)
	}
	raw = raw[:n]

	if len(raw) < SaltLen+NonceLen {
		return nil, fmt.Errorf("config: ciphertext too short: %d bytes", len(raw))
	}

	salt := raw[:SaltLen]
	nonce := raw[SaltLen : SaltLen+NonceLen]
	encData := raw[SaltLen+NonceLen:]

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("config: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("config: new GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, encData, nil)
	if err != nil {
		return nil, fmt.Errorf("config: decrypt: %w", err)
	}

	return plaintext, nil
}

// deriveKey derives a 32-byte AES key from the passphrase and salt using
// PBKDF2 with SHA-256 and 100,000 iterations.
func deriveKey(passphrase string, salt []byte) []byte {
	return pbkdf2.Key([]byte(passphrase), salt, PBKDF2Iterations, KeyLen, sha256.New)
}
