package authn_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

func TestGenerateKeyPair_roundTrips(t *testing.T) {
	privatePEM, publicPEM, err := authn.GenerateKeyPair()
	require.NoError(t, err)

	priv, err := authn.ParsePrivateKey(privatePEM)
	require.NoError(t, err)
	pub, err := authn.ParsePublicKey(publicPEM)
	require.NoError(t, err)

	msg := []byte("hello")
	require.True(t, ed25519.Verify(pub, msg, ed25519.Sign(priv, msg)))
}

func TestGenerateKeyPair_usesStandardPEMHeaders(t *testing.T) {
	privatePEM, publicPEM, err := authn.GenerateKeyPair()
	require.NoError(t, err)

	require.Contains(t, string(privatePEM), "-----BEGIN PRIVATE KEY-----")
	require.Contains(t, string(publicPEM), "-----BEGIN PUBLIC KEY-----")
}

func TestLoadKeys_readFiles(t *testing.T) {
	privatePEM, publicPEM, err := authn.GenerateKeyPair()
	require.NoError(t, err)
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.pem")
	publicPath := filepath.Join(dir, "public.pem")
	require.NoError(t, os.WriteFile(privatePath, privatePEM, 0o600))
	require.NoError(t, os.WriteFile(publicPath, publicPEM, 0o600))

	priv, err := authn.LoadPrivateKey(privatePath)
	require.NoError(t, err)
	pub, err := authn.LoadPublicKey(publicPath)
	require.NoError(t, err)

	require.True(t, priv.Public().(ed25519.PublicKey).Equal(pub))
}

func TestLoadKeys_reportProblems(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	garbage := filepath.Join(t.TempDir(), "garbage.pem")
	require.NoError(t, os.WriteFile(garbage, []byte("not a key"), 0o600))

	_, err := authn.LoadPrivateKey(missing)
	require.ErrorContains(t, err, "read private key")
	_, err = authn.LoadPublicKey(missing)
	require.ErrorContains(t, err, "read public key")

	_, err = authn.LoadPrivateKey(garbage)
	require.ErrorContains(t, err, garbage)
	require.ErrorContains(t, err, "PEM block")
}

// ecdsaKeys returns a valid but non-Ed25519 key pair in the same encoding.
func ecdsaKeys(t *testing.T) (privatePEM, publicPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
}

func TestParseKeys_rejectInvalidInput(t *testing.T) {
	privatePEM, publicPEM, err := authn.GenerateKeyPair()
	require.NoError(t, err)
	ecPrivate, ecPublic := ecdsaKeys(t)

	tests := []struct {
		name    string
		parse   func() error
		wantErr string
	}{
		{
			name:    "private key is not PEM",
			parse:   func() error { _, err := authn.ParsePrivateKey([]byte("hello")); return err },
			wantErr: "PEM block",
		},
		{
			name:    "private key slot holds a public key",
			parse:   func() error { _, err := authn.ParsePrivateKey(publicPEM); return err },
			wantErr: "PEM block",
		},
		{
			name:    "private key is not Ed25519",
			parse:   func() error { _, err := authn.ParsePrivateKey(ecPrivate); return err },
			wantErr: "not an Ed25519 key",
		},
		{
			name:    "public key slot holds a private key",
			parse:   func() error { _, err := authn.ParsePublicKey(privatePEM); return err },
			wantErr: "PEM block",
		},
		{
			name:    "public key is not Ed25519",
			parse:   func() error { _, err := authn.ParsePublicKey(ecPublic); return err },
			wantErr: "not an Ed25519 key",
		},
		{
			name:    "public key is empty",
			parse:   func() error { _, err := authn.ParsePublicKey(nil); return err },
			wantErr: "PEM block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, tt.parse(), tt.wantErr)
		})
	}
}
