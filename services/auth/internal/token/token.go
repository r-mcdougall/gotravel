package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

const refreshTokenBytes = 32

type Issuer struct {
	key ed25519.PrivateKey
	ttl time.Duration
}

func NewIssuer(key ed25519.PrivateKey, ttl time.Duration) *Issuer {
	return &Issuer{key: key, ttl: ttl}
}

func (i *Issuer) TTL() time.Duration {
	return i.ttl
}

func (i *Issuer) Issue(userID, role string) (string, error) {
	now := time.Now()

	registeredClaims := jwt.RegisteredClaims{
		Issuer:    authn.IssuerName,
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
	}

	claims := authn.AccessClaims{
		Role:             role,
		RegisteredClaims: registeredClaims,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(i.key)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}

	return signed, nil
}

func NewRefreshToken() (token string, hash []byte, err error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}

	token = base64.RawURLEncoding.EncodeToString(raw)

	return token, HashRefreshToken(token), nil
}

func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
