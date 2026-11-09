package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
	"github.com/r-mcdougall/gotravel/services/auth/internal/token"
)

const (
	userID      = "11111111-1111-1111-1111-111111111111"
	profileJSON = `{"id":"11111111-1111-1111-1111-111111111111","email":"john.doe@example.com","full_name":"John Doe",` +
		`"phone":"+6281234567890","role":"user","created_at":"2026-11-09T03:00:00Z","updated_at":"2026-11-09T03:00:00Z"}`
	tokenPairJSON = `{"access_token":"access","token_type":"Bearer","expires_in":900,"refresh_token":"refresh","refresh_expires_in":604800}`
)

var fixtureTime = time.Date(2026, time.November, 9, 3, 0, 0, 0, time.UTC)

func profileFixture() ProfileResponse {
	return ProfileResponse{
		ID:        userID,
		Email:     "john.doe@example.com",
		FullName:  "John Doe",
		Phone:     ptr("+6281234567890"),
		Role:      authn.RoleUser,
		CreatedAt: fixtureTime,
		UpdatedAt: fixtureTime,
	}
}

// fakeService replaces the use cases. A call to a method whose func is not set
// fails the test, so tests also prove that the handler did not call the service.
type fakeService struct {
	t             *testing.T
	register      func(context.Context, RegisterRequest) (RegisterResponse, error)
	login         func(context.Context, LoginRequest) (LoginResponse, error)
	refresh       func(context.Context, RefreshRequest) (RefreshResponse, error)
	logout        func(context.Context, LogoutRequest) error
	getProfile    func(context.Context, string) (ProfileResponse, error)
	updateProfile func(context.Context, string, UpdateProfileRequest) (ProfileResponse, error)
}

func (f *fakeService) Register(ctx context.Context, request RegisterRequest) (RegisterResponse, error) {
	f.t.Helper()
	if f.register == nil {
		f.t.Fatal("unexpected call to Register")
	}
	return f.register(ctx, request)
}

func (f *fakeService) Login(ctx context.Context, request LoginRequest) (LoginResponse, error) {
	f.t.Helper()
	if f.login == nil {
		f.t.Fatal("unexpected call to Login")
	}
	return f.login(ctx, request)
}

func (f *fakeService) Refresh(ctx context.Context, request RefreshRequest) (RefreshResponse, error) {
	f.t.Helper()
	if f.refresh == nil {
		f.t.Fatal("unexpected call to Refresh")
	}
	return f.refresh(ctx, request)
}

func (f *fakeService) Logout(ctx context.Context, request LogoutRequest) error {
	f.t.Helper()
	if f.logout == nil {
		f.t.Fatal("unexpected call to Logout")
	}
	return f.logout(ctx, request)
}

func (f *fakeService) GetProfile(ctx context.Context, id string) (ProfileResponse, error) {
	f.t.Helper()
	if f.getProfile == nil {
		f.t.Fatal("unexpected call to GetProfile")
	}
	return f.getProfile(ctx, id)
}

func (f *fakeService) UpdateProfile(ctx context.Context, id string, request UpdateProfileRequest) (ProfileResponse, error) {
	f.t.Helper()
	if f.updateProfile == nil {
		f.t.Fatal("unexpected call to UpdateProfile")
	}
	return f.updateProfile(ctx, id, request)
}

type unitHandler struct {
	router http.Handler
	logs   *bytes.Buffer
	issuer *token.Issuer
}

func newUnitHandler(t *testing.T, fake *fakeService) *unitHandler {
	t.Helper()
	fake.t = t
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	api := NewHandler(fake, log, authn.NewVerifier(pub))

	return &unitHandler{
		router: httpserver.NewRouter(log, metrics.New(), api.Routes),
		logs:   logs,
		issuer: token.NewIssuer(priv, accessTTL),
	}
}

func (h *unitHandler) accessToken(t *testing.T, id string) string {
	t.Helper()
	signed, err := h.issuer.Issue(id, authn.RoleUser)
	require.NoError(t, err)
	return signed
}

func (h *unitHandler) serve(t *testing.T, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return h.serveRequest(req)
}

func (h *unitHandler) serveRequest(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

const validRegisterBody = `{"email":"john.doe@example.com","password":"correct horse battery","full_name":"John Doe"}`

func TestHandlerRegister_passesRequestToServiceAndReturnsCreated(t *testing.T) {
	var got RegisterRequest
	h := newUnitHandler(t, &fakeService{register: func(_ context.Context, request RegisterRequest) (RegisterResponse, error) {
		got = request
		return profileFixture(), nil
	}})

	rec := h.serve(t, http.MethodPost, "/api/v1/auth/register",
		`{"email":"John.Doe@example.com","password":"correct horse battery","full_name":"John Doe","phone":"+6281234567890"}`, "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, profileJSON, rec.Body.String())
	require.Equal(t, RegisterRequest{
		Email:    "John.Doe@example.com",
		Password: testPassword,
		FullName: "John Doe",
		Phone:    ptr("+6281234567890"),
	}, got)
}

func TestHandlerLogin_passesRequestToServiceAndReturnsTokenPair(t *testing.T) {
	var got LoginRequest
	h := newUnitHandler(t, &fakeService{login: func(_ context.Context, request LoginRequest) (LoginResponse, error) {
		got = request
		return LoginResponse{
			AccessToken: "access", TokenType: "Bearer", ExpiresIn: 900, RefreshToken: "refresh", RefreshExpiresIn: 604800,
		}, nil
	}})

	rec := h.serve(t, http.MethodPost, "/api/v1/auth/login", `{"email":"john.doe@example.com","password":"correct horse battery"}`, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, tokenPairJSON, rec.Body.String())
	require.Equal(t, LoginRequest{Email: "john.doe@example.com", Password: testPassword}, got)
}

func TestHandler_mapsServiceErrorsToResponses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"validation error", &ValidationError{Details: []httpserver.ErrorDetail{{Field: "email", Message: "must be a valid email address"}}}, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"wrapped validation error", fmt.Errorf("validate request: %w", &ValidationError{}), http.StatusBadRequest, "VALIDATION_ERROR"},
		{"email taken", ErrEmailTaken, http.StatusConflict, "EMAIL_TAKEN"},
		{"wrapped email taken", fmt.Errorf("register: %w", ErrEmailTaken), http.StatusConflict, "EMAIL_TAKEN"},
		{"invalid credentials", ErrInvalidCredentials, http.StatusUnauthorized, "INVALID_CREDENTIALS"},
		{"user not found", ErrUserNotFound, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"unexpected error", errors.New("connection refused"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newUnitHandler(t, &fakeService{register: func(context.Context, RegisterRequest) (RegisterResponse, error) {
				return RegisterResponse{}, tt.err
			}})

			rec := h.serve(t, http.MethodPost, "/api/v1/auth/register", validRegisterBody, "")

			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			require.Equal(t, tt.wantCode, decode[errorBody](t, rec).Error.Code)
		})
	}
}

func TestHandler_listsInvalidFieldsInValidationResponse(t *testing.T) {
	h := newUnitHandler(t, &fakeService{register: func(context.Context, RegisterRequest) (RegisterResponse, error) {
		return RegisterResponse{}, fmt.Errorf("validate request: %w", &ValidationError{Details: []httpserver.ErrorDetail{
			{Field: "email", Message: "must be a valid email address"},
			{Field: "password", Message: "must be between 8 and 72 bytes"},
		}})
	}})

	rec := h.serve(t, http.MethodPost, "/api/v1/auth/register", validRegisterBody, "")

	require.JSONEq(t, `{"error":{"code":"VALIDATION_ERROR","message":"request validation failed","details":[`+
		`{"field":"email","message":"must be a valid email address"},`+
		`{"field":"password","message":"must be between 8 and 72 bytes"}]}}`, rec.Body.String())
}

func TestHandler_hidesInternalErrorsFromClientButLogsThem(t *testing.T) {
	h := newUnitHandler(t, &fakeService{register: func(context.Context, RegisterRequest) (RegisterResponse, error) {
		return RegisterResponse{}, errors.New("create user: database is down")
	}})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/register", strings.NewReader(validRegisterBody))
	req.Header.Set("X-Request-ID", "req-123")

	rec := h.serveRequest(req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.JSONEq(t, `{"error":{"code":"INTERNAL_ERROR","message":"internal server error"}}`, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "database is down")
	require.Contains(t, h.logs.String(), "database is down")
	require.Contains(t, h.logs.String(), "req-123")
}

func TestHandler_rejectsUnparsableBodiesWithoutCallingService(t *testing.T) {
	endpoints := []struct {
		name   string
		method string
		path   string
		bearer bool
	}{
		{"register", http.MethodPost, "/api/v1/auth/register", false},
		{"login", http.MethodPost, "/api/v1/auth/login", false},
		{"refresh", http.MethodPost, "/api/v1/auth/refresh", false},
		{"logout", http.MethodPost, "/api/v1/auth/logout", false},
		{"update profile", http.MethodPatch, "/api/v1/users/me", true},
	}
	bodies := map[string]string{
		"unknown field":  `{"email":"john.doe@example.com","role":"admin"}`,
		"malformed json": `{"email":`,
		"empty body":     ``,
		"not an object":  `"john.doe@example.com"`,
		"wrong type":     `{"email":42}`,
		"trailing value": `{"email":"john.doe@example.com"}{"email":"jane.smith@example.com"}`,
	}

	for _, endpoint := range endpoints {
		for name, body := range bodies {
			t.Run(endpoint.name+" "+name, func(t *testing.T) {
				h := newUnitHandler(t, &fakeService{})
				bearer := ""
				if endpoint.bearer {
					bearer = h.accessToken(t, userID)
				}

				rec := h.serve(t, endpoint.method, endpoint.path, body, bearer)

				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				require.JSONEq(t, `{"error":{"code":"VALIDATION_ERROR","message":"invalid request body"}}`, rec.Body.String())
			})
		}
	}
}

func TestHandlerUsersMe_requiresValidAccessToken(t *testing.T) {
	bearers := map[string]string{
		"no token":      "",
		"garbage token": "not-a-jwt",
	}

	for name, bearer := range bearers {
		for _, method := range []string{http.MethodGet, http.MethodPatch} {
			t.Run(name+" "+method, func(t *testing.T) {
				h := newUnitHandler(t, &fakeService{})

				rec := h.serve(t, method, "/api/v1/users/me", `{}`, bearer)

				require.Equal(t, http.StatusUnauthorized, rec.Code)
				require.Equal(t, "UNAUTHORIZED", decode[errorBody](t, rec).Error.Code)
			})
		}
	}
}

func TestHandlerGetMe_looksUpTheUserFromTheAccessToken(t *testing.T) {
	var askedFor string
	h := newUnitHandler(t, &fakeService{getProfile: func(_ context.Context, id string) (ProfileResponse, error) {
		askedFor = id
		return profileFixture(), nil
	}})

	rec := h.serve(t, http.MethodGet, "/api/v1/users/me", "", h.accessToken(t, userID))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, profileJSON, rec.Body.String())
	require.Equal(t, userID, askedFor)
}

func TestHandlerGetMe_treatsUnknownUserAsUnauthorized(t *testing.T) {
	h := newUnitHandler(t, &fakeService{getProfile: func(context.Context, string) (ProfileResponse, error) {
		return ProfileResponse{}, ErrUserNotFound
	}})

	rec := h.serve(t, http.MethodGet, "/api/v1/users/me", "", h.accessToken(t, userID))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "UNAUTHORIZED", decode[errorBody](t, rec).Error.Code)
}

func TestHandlerUpdateMe_decodesOmittedNullAndPresentFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want UpdateProfileRequest
	}{
		{"nothing sent", `{}`, UpdateProfileRequest{}},
		{"full name only", `{"full_name":"Jane Smith"}`, UpdateProfileRequest{FullName: ptr("Jane Smith")}},
		{"phone value", `{"phone":"08123456789"}`, UpdateProfileRequest{Phone: NullableString{Set: true, Value: ptr("08123456789")}}},
		{"phone null", `{"phone":null}`, UpdateProfileRequest{Phone: NullableString{Set: true}}},
		{"both fields", `{"full_name":"Jane Smith","phone":null}`, UpdateProfileRequest{FullName: ptr("Jane Smith"), Phone: NullableString{Set: true}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotID string
			var got UpdateProfileRequest
			h := newUnitHandler(t, &fakeService{updateProfile: func(_ context.Context, id string, request UpdateProfileRequest) (ProfileResponse, error) {
				gotID, got = id, request
				return profileFixture(), nil
			}})

			rec := h.serve(t, http.MethodPatch, "/api/v1/users/me", tt.body, h.accessToken(t, userID))

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.JSONEq(t, profileJSON, rec.Body.String())
			require.Equal(t, userID, gotID)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestHandlerUpdateMe_treatsUnknownUserAsUnauthorized(t *testing.T) {
	h := newUnitHandler(t, &fakeService{updateProfile: func(context.Context, string, UpdateProfileRequest) (ProfileResponse, error) {
		return ProfileResponse{}, ErrUserNotFound
	}})

	rec := h.serve(t, http.MethodPatch, "/api/v1/users/me", `{"full_name":"Jane Smith"}`, h.accessToken(t, userID))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandlerRefresh_passesTokenToServiceAndReturnsTokenPair(t *testing.T) {
	var got RefreshRequest
	h := newUnitHandler(t, &fakeService{refresh: func(_ context.Context, request RefreshRequest) (RefreshResponse, error) {
		got = request
		return RefreshResponse{
			AccessToken: "access", TokenType: "Bearer", ExpiresIn: 900, RefreshToken: "refresh", RefreshExpiresIn: 604800,
		}, nil
	}})

	rec := h.serve(t, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"old-refresh-token"}`, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, tokenPairJSON, rec.Body.String())
	require.Equal(t, RefreshRequest{RefreshToken: "old-refresh-token"}, got)
}

func TestHandlerRefresh_answersAnInvalidTokenWith401(t *testing.T) {
	h := newUnitHandler(t, &fakeService{refresh: func(context.Context, RefreshRequest) (RefreshResponse, error) {
		return RefreshResponse{}, ErrInvalidRefreshToken
	}})

	rec := h.serve(t, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"old-refresh-token"}`, "")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":{"code":"INVALID_REFRESH_TOKEN","message":"refresh token is invalid or expired"}}`, rec.Body.String())
}

func TestHandlerLogout_returnsNoContent(t *testing.T) {
	var got LogoutRequest
	h := newUnitHandler(t, &fakeService{logout: func(_ context.Context, request LogoutRequest) error {
		got = request
		return nil
	}})

	rec := h.serve(t, http.MethodPost, "/api/v1/auth/logout", `{"refresh_token":"old-refresh-token"}`, "")

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Empty(t, rec.Body.String())
	require.Equal(t, LogoutRequest{RefreshToken: "old-refresh-token"}, got)
}

func TestHandlerLogout_hidesServiceFailures(t *testing.T) {
	h := newUnitHandler(t, &fakeService{logout: func(context.Context, LogoutRequest) error {
		return errors.New("revoke refresh token: database is down")
	}})

	rec := h.serve(t, http.MethodPost, "/api/v1/auth/logout", `{"refresh_token":"old-refresh-token"}`, "")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "database is down")
}

// Integration tests below run against a real PostgreSQL container and are
// skipped by `go test -short`.

func TestRegister_createsUserWithNormalizedFields(t *testing.T) {
	e := newEnv(t)

	rec := e.do(t, http.MethodPost, "/api/v1/auth/register", registerBody(t, map[string]any{
		"email":     "  John.Doe@Example.COM ",
		"full_name": "  John Doe ",
		"phone":     "+6281234567890",
	}), "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	user := decode[userBody](t, rec)
	require.NotEmpty(t, user.ID)
	require.Equal(t, "john.doe@example.com", user.Email)
	require.Equal(t, "John Doe", user.FullName)
	require.Equal(t, "user", user.Role)
	require.NotNil(t, user.Phone)
	require.Equal(t, "+6281234567890", *user.Phone)
	require.NotContains(t, rec.Body.String(), "password")
}

func TestRegister_rejectsDuplicateEmailIgnoringCase(t *testing.T) {
	e := newEnv(t)
	e.register(t, nil)

	rec := e.do(t, http.MethodPost, "/api/v1/auth/register", registerBody(t, map[string]any{"email": "JOHN.DOE@example.com"}), "")

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "EMAIL_TAKEN", decode[errorBody](t, rec).Error.Code)
	require.Equal(t, 1, e.countUsers(t))
}

func TestLogin_returnsVerifiableTokenPair(t *testing.T) {
	e := newEnv(t)
	user := e.register(t, nil)

	rec := e.login(t, "  JOHN.DOE@example.com", testPassword)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	pair := decode[tokenBody](t, rec)
	require.Equal(t, "Bearer", pair.TokenType)
	require.EqualValues(t, accessTTL.Seconds(), pair.ExpiresIn)
	require.EqualValues(t, refreshTTL.Seconds(), pair.RefreshExpiresIn)

	identity, err := e.verifier.Verify(pair.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authn.Identity{UserID: user.ID, Role: authn.RoleUser}, identity)

	var (
		userID    string
		active    bool
		expiresAt time.Time
	)
	err = e.pool.QueryRow(t.Context(),
		"SELECT user_id, revoked_at IS NULL, expires_at FROM refresh_tokens WHERE token_hash = $1",
		token.HashRefreshToken(pair.RefreshToken),
	).Scan(&userID, &active, &expiresAt)
	require.NoError(t, err)
	require.Equal(t, user.ID, userID)
	require.True(t, active)
	require.WithinDuration(t, time.Now().Add(refreshTTL), expiresAt, time.Minute)
}

func TestLogin_failsIdenticallyForWrongPasswordAndUnknownEmail(t *testing.T) {
	e := newEnv(t)
	user := e.register(t, nil)

	wrongPassword := e.login(t, user.Email, "not the password")
	unknownEmail := e.login(t, "jane.smith@example.com", testPassword)

	require.Equal(t, http.StatusUnauthorized, wrongPassword.Code)
	require.Equal(t, http.StatusUnauthorized, unknownEmail.Code)
	require.Equal(t, "INVALID_CREDENTIALS", decode[errorBody](t, wrongPassword).Error.Code)
	require.JSONEq(t, wrongPassword.Body.String(), unknownEmail.Body.String())
}

func TestGetMe_returnsCurrentUser(t *testing.T) {
	e := newEnv(t)
	user, access := e.registerAndLogin(t, map[string]any{"phone": "+6281234567890"})

	rec := e.do(t, http.MethodGet, "/api/v1/users/me", "", access)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[userBody](t, rec)
	require.Equal(t, user.ID, got.ID)
	require.Equal(t, "john.doe@example.com", got.Email)
	require.Equal(t, "+6281234567890", *got.Phone)
	require.NotContains(t, rec.Body.String(), "password")
}

func TestUsersMe_rejectsTokenOfDeletedUser(t *testing.T) {
	e := newEnv(t)
	user, access := e.registerAndLogin(t, nil)
	_, err := e.pool.Exec(t.Context(), "DELETE FROM users WHERE id = $1", user.ID)
	require.NoError(t, err)

	get := e.do(t, http.MethodGet, "/api/v1/users/me", "", access)
	patch := e.do(t, http.MethodPatch, "/api/v1/users/me", `{"full_name":"Jane Smith"}`, access)

	require.Equal(t, http.StatusUnauthorized, get.Code)
	require.Equal(t, http.StatusUnauthorized, patch.Code)
}

func TestUpdateMe_changesOnlyProvidedFields(t *testing.T) {
	e := newEnv(t)
	_, access := e.registerAndLogin(t, map[string]any{"phone": "+6281234567890"})

	rec := e.do(t, http.MethodPatch, "/api/v1/users/me", `{"full_name":"  Jane Smith "}`, access)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[userBody](t, rec)
	require.Equal(t, "Jane Smith", got.FullName)
	require.NotNil(t, got.Phone)
	require.Equal(t, "+6281234567890", *got.Phone)
	require.True(t, got.UpdatedAt.After(got.CreatedAt))
}

func TestUpdateMe_clearsPhoneWhenNull(t *testing.T) {
	e := newEnv(t)
	_, access := e.registerAndLogin(t, map[string]any{"phone": "+6281234567890"})

	rec := e.do(t, http.MethodPatch, "/api/v1/users/me", `{"phone":null}`, access)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, decode[userBody](t, rec).Phone)
}

func TestRefreshAndLogout_endToEnd(t *testing.T) {
	e := newEnv(t)
	e.register(t, nil)
	first := decode[tokenBody](t, e.login(t, "john.doe@example.com", testPassword))

	refreshed := e.do(t, http.MethodPost, "/api/v1/auth/refresh", toJSON(t, map[string]string{"refresh_token": first.RefreshToken}), "")
	require.Equal(t, http.StatusOK, refreshed.Code, refreshed.Body.String())
	second := decode[tokenBody](t, refreshed)
	require.NotEqual(t, first.RefreshToken, second.RefreshToken)

	loggedOut := e.do(t, http.MethodPost, "/api/v1/auth/logout", toJSON(t, map[string]string{"refresh_token": second.RefreshToken}), "")
	require.Equal(t, http.StatusNoContent, loggedOut.Code)
	require.Empty(t, loggedOut.Body.String())

	reused := e.do(t, http.MethodPost, "/api/v1/auth/refresh", toJSON(t, map[string]string{"refresh_token": second.RefreshToken}), "")
	require.Equal(t, http.StatusUnauthorized, reused.Code)
	require.Equal(t, "INVALID_REFRESH_TOKEN", decode[errorBody](t, reused).Error.Code)
}
