package authn

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

const IssuerName = "gotravel-auth"

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

var ErrInvalidToken = errors.New("invalid token")

type AccessClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

type Identity struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type Verifier struct {
	key ed25519.PublicKey
}

func NewVerifier(key ed25519.PublicKey) *Verifier {
	return &Verifier{key: key}
}

func (v *Verifier) Verify(tokenStr string) (Identity, error) {
	var claims AccessClaims
	_, err := jwt.ParseWithClaims(tokenStr,
		&claims,
		func(token *jwt.Token) (any, error) { return v.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(IssuerName),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if claims.Subject == "" {
		return Identity{}, fmt.Errorf("%w: missing subject", ErrInvalidToken)
	}
	if claims.Role != RoleUser && claims.Role != RoleAdmin {
		return Identity{}, fmt.Errorf("%w: unknown role %q", ErrInvalidToken, claims.Role)
	}

	return Identity{UserID: claims.Subject, Role: claims.Role}, err
}
