package config

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Parallel()

	passphrase := "my-secret-passphrase-123!"
	plaintext := []byte(`{"mysql":{"host":"127.0.0.1","port":3306,"user":"root","password":"s3cret","database":"mydb"}}`)

	encrypted, err := Encrypt(plaintext, passphrase)
	require.NoError(t, err, "Encrypt should succeed")
	require.NotEmpty(t, encrypted, "encrypted output should not be empty")

	// Output should be hex-encoded (ASCII alphanumeric chars + lowercase a-f)
	decoded, err := hex.DecodeString(string(encrypted))
	require.NoError(t, err, "encrypted output should be valid hex")
	// 16 salt + 12 nonce + len(plaintext) + 16 GCM tag
	expectedMinLen := SaltLen + NonceLen + len(plaintext)
	require.GreaterOrEqual(t, len(decoded), expectedMinLen, "encrypted payload should be at least salt+nonce+plaintext size")

	// Decrypt
	decrypted, err := Decrypt(encrypted, passphrase)
	require.NoError(t, err, "Decrypt should succeed")
	require.Equal(t, plaintext, decrypted, "decrypted text should match original")
}

func TestEncryptDecrypt_WrongPassphrase(t *testing.T) {
	t.Parallel()

	plaintext := []byte("hello world")
	encrypted, err := Encrypt(plaintext, "correct-passphrase")
	require.NoError(t, err)

	_, err = Decrypt(encrypted, "wrong-passphrase")
	require.Error(t, err, "decrypting with wrong passphrase should fail")
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	t.Parallel()

	encrypted, err := Encrypt([]byte{}, "pass")
	require.NoError(t, err)

	decrypted, err := Decrypt(encrypted, "pass")
	require.NoError(t, err)
	require.Empty(t, decrypted, "decrypted empty plaintext should be empty")
}

func TestEncryptDecrypt_EmptyPassphrase(t *testing.T) {
	t.Parallel()

	plaintext := []byte("some config data")
	encrypted, err := Encrypt(plaintext, "")
	require.NoError(t, err, "empty passphrase should still work")

	decrypted, err := Decrypt(encrypted, "")
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)
}

func TestDecrypt_InvalidHex(t *testing.T) {
	t.Parallel()

	_, err := Decrypt([]byte("this-is-not-hex!!!!"), "pass")
	require.Error(t, err, "invalid hex should fail")
}

func TestDecrypt_TooShort(t *testing.T) {
	t.Parallel()

	// Hex-encode something shorter than salt + nonce
	short := make([]byte, 10)
	encoded := hex.EncodeToString(short)
	_, err := Decrypt([]byte(encoded), "pass")
	require.Error(t, err, "too-short ciphertext should fail")
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	t.Parallel()

	plaintext := []byte("sensitive data")
	encrypted, err := Encrypt(plaintext, "passphrase")
	require.NoError(t, err)

	// Flip a byte in the hex representation (middle of the ciphertext portion)
	// to simulate tampering. The hex string length is 2 * raw length.
	if len(encrypted) > 10 {
		tampered := make([]byte, len(encrypted))
		copy(tampered, encrypted)
		// Flip a nibble in the hex string
		tampered[len(tampered)/2] ^= 0x01
		_, err = Decrypt(tampered, "passphrase")
		require.Error(t, err, "tampered ciphertext should fail authentication")
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	t.Parallel()

	salt := []byte("0123456789abcdef")
	k1 := deriveKey("passphrase", salt)
	k2 := deriveKey("passphrase", salt)
	require.Equal(t, k1, k2, "same inputs should produce same key")

	k3 := deriveKey("different-passphrase", salt)
	require.NotEqual(t, k1, k3, "different passphrase should produce different key")

	k4 := deriveKey("passphrase", []byte("abcdefghijklmnop"))
	require.NotEqual(t, k1, k4, "different salt should produce different key")
}

func TestEncrypt_UniqueNonce(t *testing.T) {
	t.Parallel()

	// Encrypting the same data twice should produce different outputs
	// because the nonce (and salt) are random each time.
	plaintext := []byte("same data")

	e1, err := Encrypt(plaintext, "pass")
	require.NoError(t, err)
	e2, err := Encrypt(plaintext, "pass")
	require.NoError(t, err)

	require.NotEqual(t, e1, e2, "two encryptions of the same data should differ")
}
