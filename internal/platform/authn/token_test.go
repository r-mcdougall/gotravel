package authn_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

func newKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func validClaims() authn.AccessClaims {
	now := time.Now()
	return authn.AccessClaims{
		Role: authn.RoleUser,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authn.IssuerName,
			Subject:   "user-1",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
}

func sign(t *testing.T, method jwt.SigningMethod, key any, claims jwt.Claims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
	require.NoError(t, err)
	return signed
}

func TestVerifier_acceptsValidTokens(t *testing.T) {
	pub, priv := newKeyPair(t)
	verifier := authn.NewVerifier(pub)

	user := validClaims()
	got, err := verifier.Verify(sign(t, jwt.SigningMethodEdDSA, priv, user))
	require.NoError(t, err)
	require.Equal(t, authn.Identity{UserID: "user-1", Role: authn.RoleUser}, got)

	admin := validClaims()
	admin.Role = authn.RoleAdmin
	got, err = verifier.Verify(sign(t, jwt.SigningMethodEdDSA, priv, admin))
	require.NoError(t, err)
	require.Equal(t, authn.RoleAdmin, got.Role)
}

func TestVerifier_rejectsInvalidTokens(t *testing.T) {
	pub, priv := newKeyPair(t)
	_, otherPriv := newKeyPair(t)
	verifier := authn.NewVerifier(pub)

	withClaims := func(mutate func(*authn.AccessClaims)) string {
		claims := validClaims()
		mutate(&claims)
		return sign(t, jwt.SigningMethodEdDSA, priv, claims)
	}

	tests := []struct {
		name  string
		token string
	}{
		{"expired", withClaims(func(c *authn.AccessClaims) {
			c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
		})},
		{"missing expiration", withClaims(func(c *authn.AccessClaims) { c.ExpiresAt = nil })},
		{"wrong issuer", withClaims(func(c *authn.AccessClaims) { c.Issuer = "someone-else" })},
		{"empty subject", withClaims(func(c *authn.AccessClaims) { c.Subject = "" })},
		{"unknown role", withClaims(func(c *authn.AccessClaims) { c.Role = "superuser" })},
		{"signed by another key", sign(t, jwt.SigningMethodEdDSA, otherPriv, validClaims())},
		{"HS256 with the public key as secret", sign(t, jwt.SigningMethodHS256, []byte(pub), validClaims())},
		{"alg none", sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims())},
		{"garbage", "not-a-token"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verifier.Verify(tt.token)

			require.ErrorIs(t, err, authn.ErrInvalidToken)
		})
	}
}
