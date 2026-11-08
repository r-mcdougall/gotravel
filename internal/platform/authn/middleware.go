package authn

import (
	"context"
	"net/http"
	"strings"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

const bearerPrefix = "bearer "

type identityKey struct{}

func Authenticate(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w)
				return
			}

			identity, err := v.Verify(token)
			if err != nil {
				unauthorized(w)
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity)))
		})
	}
}

func RequireRole(role string) func(handler http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := IdentityFrom(r.Context())
			if !ok {
				unauthorized(w)
				return
			}

			if identity.Role != role {
				httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "insufficient permissions")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if len(header) <= len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}

	token := strings.TrimSpace(header[len(bearerPrefix):])
	return token, token != ""
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid access token")
}
