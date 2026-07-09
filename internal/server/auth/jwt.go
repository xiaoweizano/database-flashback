package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AccessTokenDuration is the lifetime of an access token.
	AccessTokenDuration = 24 * time.Hour
	// RefreshTokenDuration is the lifetime of a refresh token.
	RefreshTokenDuration = 7 * 24 * time.Hour
	// BcryptCost is the bcrypt hashing cost used for passwords.
	BcryptCost = 12
)

// Claims represents the JWT claims embedded in access tokens.
type Claims struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// CreateToken signs a JWT access token for the given user.
func CreateToken(userID, email string, secret []byte) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenDuration)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "mysql-pitr",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// CreateRefreshToken signs a JWT refresh token for the given user.
func CreateRefreshToken(userID string, secret []byte) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTokenDuration)),
		IssuedAt:  jwt.NewNumericDate(now),
		Issuer:    "mysql-pitr",
		Subject:   userID,
		ID:        "refresh",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ValidateToken parses and validates a JWT access token, returning the embedded
// claims on success.
func ValidateToken(tokenStr string, secret []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{},
		func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v",
					token.Header["alg"])
			}
			return secret, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// ValidateRefreshToken parses and validates a refresh token, returning the
// subject (user ID) embedded in the token.
func ValidateRefreshToken(tokenStr string, secret []byte) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{},
		func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v",
					token.Header["alg"])
			}
			return secret, nil
		},
	)
	if err != nil {
		return "", fmt.Errorf("validate refresh token: %w", err)
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid refresh token")
	}
	if claims.ID != "refresh" {
		return "", fmt.Errorf("token is not a refresh token")
	}
	return claims.Subject, nil
}
