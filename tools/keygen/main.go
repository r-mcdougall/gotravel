package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

const defaultDir = "deploy/keys"

func main() {
	if err := run(defaultDir); err != nil {
		fmt.Fprintln(os.Stderr, "keygen:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	privatePath := filepath.Join(dir, "jwt_private.pem")
	publicPath := filepath.Join(dir, "jwt_public.pem")

	for _, path := range []string{privatePath, publicPath} {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; delete both key files to rotate", path)
		}
	}

	privatePEM, publicPEM, err := authn.GenerateKeyPair()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := writeNew(privatePath, privatePEM); err != nil {
		return err
	}
	if err := writeNew(publicPath, publicPEM); err != nil {
		return err
	}

	fmt.Printf("wrote %s and %s\n", privatePath, publicPath)
	return nil
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // dev key must be readable by the non-root container user
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
