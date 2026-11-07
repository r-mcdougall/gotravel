package token_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/services/auth/internal/token"
)

func newKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func TestIssuer_issuesTokenThatTheVerifierAccepts(t *testing.T) {
	pub, priv := newKeyPair(t)
	issuer := token.NewIssuer(priv, 15*time.Minute)

	signed, err := issuer.Issue("user-1", authn.RoleAdmin)
	require.NoError(t, err)

	got, err := authn.NewVerifier(pub).Verify(signed)
	require.NoError(t, err)
	require.Equal(t, authn.Identity{UserID: "user-1", Role: authn.RoleAdmin}, got)
}

func TestIssuer_tokenIsRejectedOnceTTLHasPassed(t *testing.T) {
	pub, priv := newKeyPair(t)
	issuer := token.NewIssuer(priv, -time.Second)

	signed, err := issuer.Issue("user-1", authn.RoleUser)
	require.NoError(t, err)

	_, err = authn.NewVerifier(pub).Verify(signed)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
}

func TestIssuer_tokenSignedByAnotherKeyIsRejected(t *testing.T) {
	_, priv := newKeyPair(t)
	otherPub, _ := newKeyPair(t)
	issuer := token.NewIssuer(priv, time.Minute)

	signed, err := issuer.Issue("user-1", authn.RoleUser)
	require.NoError(t, err)

	_, err = authn.NewVerifier(otherPub).Verify(signed)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
}

func TestIssuer_TTL(t *testing.T) {
	_, priv := newKeyPair(t)

	require.Equal(t, 15*time.Minute, token.NewIssuer(priv, 15*time.Minute).TTL())
}

func TestNewRefreshToken_returnsRandomTokenAndMatchingHash(t *testing.T) {
	first, firstHash, err := token.NewRefreshToken()
	require.NoError(t, err)
	second, _, err := token.NewRefreshToken()
	require.NoError(t, err)

	raw, err := base64.RawURLEncoding.DecodeString(first)
	require.NoError(t, err)
	require.Len(t, raw, 32)
	require.NotEqual(t, first, second)
	require.Len(t, firstHash, 32)
	require.Equal(t, firstHash, token.HashRefreshToken(first))
}

func TestHashRefreshToken_differsPerToken(t *testing.T) {
	require.Equal(t, token.HashRefreshToken("a"), token.HashRefreshToken("a"))
	require.NotEqual(t, token.HashRefreshToken("a"), token.HashRefreshToken("b"))
}
