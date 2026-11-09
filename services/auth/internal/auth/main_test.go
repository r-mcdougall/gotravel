package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
	"github.com/r-mcdougall/gotravel/services/auth/internal/token"
	"github.com/r-mcdougall/gotravel/services/auth/migrations"
)

// The PostgreSQL container starts on the first integration test that needs it,
// so unit tests run without Docker. `go test -short` skips integration tests.
var integrationDB struct {
	once      sync.Once
	url       string
	err       error
	terminate func()
}

func TestMain(m *testing.M) {
	code := m.Run()
	if integrationDB.terminate != nil {
		integrationDB.terminate()
	}
	os.Exit(code)
}

func databaseURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}

	integrationDB.once.Do(func() {
		ctx := context.Background()
		url, terminate, err := dbtest.Start(ctx)
		if err != nil {
			integrationDB.err = err
			return
		}
		integrationDB.terminate = terminate

		if err := database.Migrate(ctx, slog.New(slog.DiscardHandler), url, migrations.FS); err != nil {
			integrationDB.err = err
			return
		}
		integrationDB.url = url
	})
	require.NoError(t, integrationDB.err)
	return integrationDB.url
}

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 7 * 24 * time.Hour

	testPassword = "correct horse battery"
)

type env struct {
	handler  http.Handler
	service  Service
	pool     *pgxpool.Pool
	verifier *authn.Verifier
}

func newEnv(t *testing.T) *env {
	t.Helper()

	pool, err := database.Connect(t.Context(), databaseURL(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(t.Context(), "TRUNCATE users CASCADE")
	require.NoError(t, err)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	verifier := authn.NewVerifier(pub)

	log := slog.New(slog.DiscardHandler)
	svc := NewService(NewPgStore(pool), token.NewIssuer(priv, accessTTL), refreshTTL)
	api := NewHandler(svc, log, verifier)

	return &env{
		handler:  httpserver.NewRouter(log, metrics.New(), api.Routes),
		service:  svc,
		pool:     pool,
		verifier: verifier,
	}
}

func (e *env) do(t *testing.T, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

type userBody struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Phone     *string   `json:"phone"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type tokenBody struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())
	return v
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func registerBody(t *testing.T, overrides map[string]any) string {
	t.Helper()
	body := map[string]any{
		"email":     "john.doe@example.com",
		"password":  testPassword,
		"full_name": "John Doe",
	}
	for k, v := range overrides {
		body[k] = v
	}
	return toJSON(t, body)
}

func (e *env) register(t *testing.T, overrides map[string]any) userBody {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/v1/auth/register", registerBody(t, overrides), "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return decode[userBody](t, rec)
}

func (e *env) login(t *testing.T, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := toJSON(t, map[string]string{"email": email, "password": password})
	return e.do(t, http.MethodPost, "/api/v1/auth/login", body, "")
}

// registerAndLogin creates a user and returns it with a valid access token.
func (e *env) registerAndLogin(t *testing.T, overrides map[string]any) (userBody, string) {
	t.Helper()
	user := e.register(t, overrides)
	rec := e.login(t, user.Email, testPassword)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return user, decode[tokenBody](t, rec).AccessToken
}

func (e *env) countUsers(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, e.pool.QueryRow(t.Context(), "SELECT count(*) FROM users").Scan(&n))
	return n
}

func ptr[T any](v T) *T {
	return &v
}

func validRegister() RegisterRequest {
	return RegisterRequest{
		Email:    "john.doe@example.com",
		Password: testPassword,
		FullName: "John Doe",
	}
}

func validationFields(t *testing.T, err error) []string {
	t.Helper()
	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
	fields := make([]string, 0, len(validationErr.Details))
	for _, d := range validationErr.Details {
		fields = append(fields, d.Field)
	}
	return fields
}
