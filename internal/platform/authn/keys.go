// Package authn handles JWT signing keys and access token verification.
package authn

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

const (
	privateKeyBlock = "PRIVATE KEY"
	publicKeyBlock  = "PUBLIC KEY"
)

func GenerateKeyPair() (privatePEM, publicPEM []byte, err error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}

	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal private key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal public key: %w", err)
	}

	return encode(privateKeyBlock, privateDER), encode(publicKeyBlock, publicDER), nil
}

func ParsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	der, err := decode(privateKeyBlock, data)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}

	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}

	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key: not an Ed25519 key")
	}
	return private, nil
}

func ParsePublicKey(data []byte) (ed25519.PublicKey, error) {
	der, err := decode(publicKeyBlock, data)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}

	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}

	public, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key: not an Ed25519 key")
	}

	return public, nil
}

func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from trusted service configuration
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}

	key, err := ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return key, nil
}

func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from trusted service configuration
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}

	key, err := ParsePublicKey(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return key, nil
}

func encode(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

func decode(blockType string, data []byte) ([]byte, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != blockType {
		return nil, fmt.Errorf("expected a %q PEM block", blockType)
	}
	return block.Bytes, nil
}
