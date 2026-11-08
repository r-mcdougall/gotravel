package authn_test

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

const (
	unauthorizedBody = `{"error":{"code":"UNAUTHORIZED","message":"missing or invalid access token"}}`
	forbiddenBody    = `{"error":{"code":"FORBIDDEN","message":"insufficient permissions"}}`
)

func serve(t *testing.T, handler http.Handler, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func tokenFor(t *testing.T, priv ed25519.PrivateKey, role string) string {
	t.Helper()
	claims := validClaims()
	claims.Role = role
	return sign(t, jwt.SigningMethodEdDSA, priv, claims)
}

func TestAuthenticate_storesIdentityForValidToken(t *testing.T) {
	pub, priv := newKeyPair(t)
	signed := tokenFor(t, priv, authn.RoleUser)

	for _, header := range []string{"Bearer " + signed, "bearer " + signed, "BEARER   " + signed} {
		var got authn.Identity
		var found bool
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, found = authn.IdentityFrom(r.Context())
			w.WriteHeader(http.StatusNoContent)
		})

		rec := serve(t, authn.Authenticate(authn.NewVerifier(pub))(next), header)

		require.Equal(t, http.StatusNoContent, rec.Code, header)
		require.True(t, found)
		require.Equal(t, authn.Identity{UserID: "user-1", Role: authn.RoleUser}, got)
	}
}

func TestAuthenticate_rejectsMissingOrInvalidCredentials(t *testing.T) {
	pub, priv := newKeyPair(t)
	signed := tokenFor(t, priv, authn.RoleUser)

	tests := map[string]string{
		"no header":          "",
		"wrong scheme":       "Basic abc",
		"scheme only":        "Bearer",
		"scheme and space":   "Bearer ",
		"not a token":        "Bearer not-a-token",
		"token without type": signed,
	}

	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

			rec := serve(t, authn.Authenticate(authn.NewVerifier(pub))(next), header)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.JSONEq(t, unauthorizedBody, rec.Body.String())
			require.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
			require.False(t, called)
		})
	}
}

func TestRequireRole(t *testing.T) {
	pub, priv := newKeyPair(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	adminOnly := authn.Authenticate(authn.NewVerifier(pub))(authn.RequireRole(authn.RoleAdmin)(next))

	rec := serve(t, adminOnly, "Bearer "+tokenFor(t, priv, authn.RoleAdmin))
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = serve(t, adminOnly, "Bearer "+tokenFor(t, priv, authn.RoleUser))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, forbiddenBody, rec.Body.String())
}

func TestRequireRole_withoutAuthenticateRejectsRequest(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	rec := serve(t, authn.RequireRole(authn.RoleAdmin)(next), "")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, unauthorizedBody, rec.Body.String())
}
