package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

func TestRun_writesLoadableKeyPair(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")

	require.NoError(t, run(dir))

	priv, err := authn.LoadPrivateKey(filepath.Join(dir, "jwt_private.pem"))
	require.NoError(t, err)
	pub, err := authn.LoadPublicKey(filepath.Join(dir, "jwt_public.pem"))
	require.NoError(t, err)
	require.True(t, priv.Public().(ed25519.PublicKey).Equal(pub))
}

func TestRun_refusesToOverwriteExistingKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, run(dir))
	before, err := os.ReadFile(filepath.Join(dir, "jwt_private.pem"))
	require.NoError(t, err)

	err = run(dir)

	require.ErrorContains(t, err, "already exists")
	after, err := os.ReadFile(filepath.Join(dir, "jwt_private.pem"))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestRun_refusesWhenOnlyOneKeyExists(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "jwt_public.pem"), []byte("x"), 0o600))

	err := run(dir)

	require.ErrorContains(t, err, "already exists")
	require.NoFileExists(t, filepath.Join(dir, "jwt_private.pem"))
}
