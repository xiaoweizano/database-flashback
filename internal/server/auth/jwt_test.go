package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testSecret = []byte("test-secret-key-for-unit-tests")

func TestCreateAndValidateToken(t *testing.T) {
	token, err := CreateToken("user-1", "alice@example.com", testSecret)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := ValidateToken(token, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.UserID)
	assert.Equal(t, "alice@example.com", claims.Email)
	assert.Equal(t, "mysql-pitr", claims.Issuer)
	assert.True(t, claims.ExpiresAt.Time.After(time.Now()))
}

func TestValidateToken_WrongSecret(t *testing.T) {
	token, err := CreateToken("user-1", "alice@example.com", testSecret)
	require.NoError(t, err)

	_, err = ValidateToken(token, []byte("wrong-secret"))
	assert.Error(t, err)
}

func TestValidateToken_InvalidToken(t *testing.T) {
	_, err := ValidateToken("not-a-valid-jwt", testSecret)
	assert.Error(t, err)
}

func TestValidateToken_TamperedToken(t *testing.T) {
	token, err := CreateToken("user-1", "alice@example.com", testSecret)
	require.NoError(t, err)

	// Corrupt the payload section of the JWT.
	parts := []byte(token)
	parts[len(parts)/2] ^= 0xFF

	_, err = ValidateToken(string(parts), testSecret)
	assert.Error(t, err)
}

func TestCreateAndValidateRefreshToken(t *testing.T) {
	token, err := CreateRefreshToken("user-1", testSecret)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	userID, err := ValidateRefreshToken(token, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "user-1", userID)
}

func TestValidateRefreshToken_AccessTokenRejected(t *testing.T) {
	accessToken, err := CreateToken("user-1", "a@b.com", testSecret)
	require.NoError(t, err)

	_, err = ValidateRefreshToken(accessToken, testSecret)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a refresh token")
}

func TestValidateRefreshToken_WrongSecret(t *testing.T) {
	token, err := CreateRefreshToken("user-1", testSecret)
	require.NoError(t, err)

	_, err = ValidateRefreshToken(token, []byte("wrong-secret"))
	assert.Error(t, err)
}

func TestValidateToken_Expired(t *testing.T) {
	// Build an expired token directly to test expiry validation.
	now := time.Now()
	claims := Claims{
		UserID: "user-1",
		Email:  "a@b.com",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			Issuer:    "mysql-pitr",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(testSecret)
	require.NoError(t, err)

	_, err = ValidateToken(tokenStr, testSecret)
	assert.Error(t, err)
}

func TestValidateRefreshToken_Expired(t *testing.T) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
		Issuer:    "mysql-pitr",
		Subject:   "user-1",
		ID:        "refresh",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(testSecret)
	require.NoError(t, err)

	_, err = ValidateRefreshToken(tokenStr, testSecret)
	assert.Error(t, err)
}
