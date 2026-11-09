package auth

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/services/auth/internal/store"
)

type fakeAdminStore struct {
	t                   *testing.T
	createAdminIfAbsent func(context.Context, store.CreateAdminIfAbsentParams) (int64, error)
	getUserByEmail      func(context.Context, string) (store.User, error)
}

func (f *fakeAdminStore) CreateAdminIfAbsent(ctx context.Context, arg store.CreateAdminIfAbsentParams) (int64, error) {
	f.t.Helper()
	if f.createAdminIfAbsent == nil {
		f.t.Fatal("unexpected call to CreateAdminIfAbsent")
	}
	return f.createAdminIfAbsent(ctx, arg)
}

func (f *fakeAdminStore) GetUserByEmail(ctx context.Context, email string) (store.User, error) {
	f.t.Helper()
	if f.getUserByEmail == nil {
		f.t.Fatal("unexpected call to GetUserByEmail")
	}
	return f.getUserByEmail(ctx, email)
}

func newBufferLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

const adminEmail = "admin@example.com"

func TestBootstrapAdmin_doesNothingWhenNoCredentialsAreGiven(t *testing.T) {
	log, buf := newBufferLogger()

	err := BootstrapAdmin(t.Context(), &fakeAdminStore{t: t}, log, "", "")

	require.NoError(t, err)
	require.Empty(t, buf.String())
}

func TestBootstrapAdmin_createsAdminWithNormalizedEmailAndHashedPassword(t *testing.T) {
	var got store.CreateAdminIfAbsentParams
	db := &fakeAdminStore{t: t, createAdminIfAbsent: func(_ context.Context, arg store.CreateAdminIfAbsentParams) (int64, error) {
		got = arg
		return 1, nil
	}}
	log, buf := newBufferLogger()

	err := BootstrapAdmin(t.Context(), db, log, "  Admin@Example.com ", testPassword)

	require.NoError(t, err)
	require.Equal(t, adminEmail, got.Email)
	require.Equal(t, adminFullName, got.FullName)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte(testPassword)))
	require.Contains(t, buf.String(), `"msg":"admin created successfully"`)
	require.NotContains(t, buf.String(), testPassword)
}

func TestBootstrapAdmin_keepsExistingAdmin(t *testing.T) {
	db := &fakeAdminStore{
		t:                   t,
		createAdminIfAbsent: func(context.Context, store.CreateAdminIfAbsentParams) (int64, error) { return 0, nil },
		getUserByEmail: func(context.Context, string) (store.User, error) {
			return store.User{Email: adminEmail, Role: authn.RoleAdmin}, nil
		},
	}
	log, buf := newBufferLogger()

	err := BootstrapAdmin(t.Context(), db, log, adminEmail, testPassword)

	require.NoError(t, err)
	require.Contains(t, buf.String(), `"msg":"admin already exists"`)
	require.NotContains(t, buf.String(), `"level":"WARN"`)
}

func TestBootstrapAdmin_warnsWhenEmailBelongsToRegularUser(t *testing.T) {
	db := &fakeAdminStore{
		t:                   t,
		createAdminIfAbsent: func(context.Context, store.CreateAdminIfAbsentParams) (int64, error) { return 0, nil },
		getUserByEmail: func(context.Context, string) (store.User, error) {
			return store.User{Email: adminEmail, Role: authn.RoleUser}, nil
		},
	}
	log, buf := newBufferLogger()

	err := BootstrapAdmin(t.Context(), db, log, adminEmail, testPassword)

	require.NoError(t, err)
	require.Contains(t, buf.String(), `"level":"WARN"`)
	require.Contains(t, buf.String(), "not promoted")
}

func TestBootstrapAdmin_rejectsInvalidCredentialsWithoutTouchingStore(t *testing.T) {
	tests := []struct {
		name            string
		email, password string
		wantField       string
	}{
		{"invalid email", "not-an-email", testPassword, "email"},
		{"short password", adminEmail, "short", "password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _ := newBufferLogger()

			err := BootstrapAdmin(t.Context(), &fakeAdminStore{t: t}, log, tt.email, tt.password)

			require.Equal(t, []string{tt.wantField}, validationFields(t, err))
		})
	}
}

func TestBootstrapAdmin_rejectsHalfSetCredentialsWithoutTouchingStore(t *testing.T) {
	for name, credentials := range map[string][2]string{
		"email only":    {adminEmail, ""},
		"password only": {"", testPassword},
	} {
		t.Run(name, func(t *testing.T) {
			log, _ := newBufferLogger()

			err := BootstrapAdmin(t.Context(), &fakeAdminStore{t: t}, log, credentials[0], credentials[1])

			require.ErrorContains(t, err, "must be set together")
		})
	}
}

func TestBootstrapAdmin_wrapsStoreFailures(t *testing.T) {
	tests := map[string]*fakeAdminStore{
		"create fails": {
			createAdminIfAbsent: func(context.Context, store.CreateAdminIfAbsentParams) (int64, error) { return 0, errBoom },
		},
		"lookup fails": {
			createAdminIfAbsent: func(context.Context, store.CreateAdminIfAbsentParams) (int64, error) { return 0, nil },
			getUserByEmail:      func(context.Context, string) (store.User, error) { return store.User{}, errBoom },
		},
	}

	for name, db := range tests {
		t.Run(name, func(t *testing.T) {
			db.t = t
			log, _ := newBufferLogger()

			err := BootstrapAdmin(t.Context(), db, log, adminEmail, testPassword)

			require.ErrorIs(t, err, errBoom)
		})
	}
}

// Integration tests below run against a real PostgreSQL container and are
// skipped by `go test -short`.

func TestBootstrapAdmin_createdAdminCanLogInWithAdminRole(t *testing.T) {
	e := newEnv(t)
	log, _ := newBufferLogger()

	require.NoError(t, BootstrapAdmin(t.Context(), NewPgStore(e.pool), log, adminEmail, testPassword))

	session, err := e.service.Login(t.Context(), LoginRequest{Email: adminEmail, Password: testPassword})
	require.NoError(t, err)
	identity, err := e.verifier.Verify(session.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authn.RoleAdmin, identity.Role)
}

func TestBootstrapAdmin_runningTwiceKeepsTheFirstPassword(t *testing.T) {
	e := newEnv(t)
	log, _ := newBufferLogger()
	db := NewPgStore(e.pool)
	require.NoError(t, BootstrapAdmin(t.Context(), db, log, adminEmail, testPassword))

	require.NoError(t, BootstrapAdmin(t.Context(), db, log, adminEmail, "another password"))

	_, err := e.service.Login(t.Context(), LoginRequest{Email: adminEmail, Password: testPassword})
	require.NoError(t, err)
	_, err = e.service.Login(t.Context(), LoginRequest{Email: adminEmail, Password: "another password"})
	require.ErrorIs(t, err, ErrInvalidCredentials)
	require.Equal(t, 1, e.countUsers(t))
}

func TestBootstrapAdmin_doesNotPromoteRegularUser(t *testing.T) {
	e := newEnv(t)
	log, buf := newBufferLogger()
	request := validRegister()
	request.Email = adminEmail
	e.registerUser(t, request)

	require.NoError(t, BootstrapAdmin(t.Context(), NewPgStore(e.pool), log, adminEmail, "another password"))

	var role string
	require.NoError(t, e.pool.QueryRow(t.Context(), "SELECT role FROM users WHERE email = $1", adminEmail).Scan(&role))
	require.Equal(t, authn.RoleUser, role)
	require.Contains(t, buf.String(), `"level":"WARN"`)
}
