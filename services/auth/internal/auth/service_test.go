package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/services/auth/internal/store"
	"github.com/r-mcdougall/gotravel/services/auth/internal/token"
)

// fakeStore replaces the database. A call to a method whose func is not set
// fails the test, so tests also prove which calls the service does not make.
type fakeStore struct {
	t                  *testing.T
	createUser         func(context.Context, store.CreateUserParams) (store.User, error)
	getUserByEmail     func(context.Context, string) (store.User, error)
	getUserByID        func(context.Context, string) (store.User, error)
	updateUserProfile  func(context.Context, store.UpdateUserProfileParams) (store.User, error)
	createRefreshToken func(context.Context, store.CreateRefreshTokenParams) error

	rotateRefreshToken     func(context.Context, []byte) (string, error)
	getRefreshToken        func(context.Context, []byte) (store.RefreshToken, error)
	revokeRefreshToken     func(context.Context, []byte) error
	revokeAllRefreshTokens func(context.Context, string) error

	transactions int
	committed    bool // whether the last transaction ended without an error
}

func (f *fakeStore) CreateUser(ctx context.Context, arg store.CreateUserParams) (store.User, error) {
	f.t.Helper()
	if f.createUser == nil {
		f.t.Fatal("unexpected call to CreateUser")
	}
	return f.createUser(ctx, arg)
}

func (f *fakeStore) GetUserByEmail(ctx context.Context, email string) (store.User, error) {
	f.t.Helper()
	if f.getUserByEmail == nil {
		f.t.Fatal("unexpected call to GetUserByEmail")
	}
	return f.getUserByEmail(ctx, email)
}

func (f *fakeStore) GetUserByID(ctx context.Context, id string) (store.User, error) {
	f.t.Helper()
	if f.getUserByID == nil {
		f.t.Fatal("unexpected call to GetUserByID")
	}
	return f.getUserByID(ctx, id)
}

func (f *fakeStore) UpdateUserProfile(ctx context.Context, arg store.UpdateUserProfileParams) (store.User, error) {
	f.t.Helper()
	if f.updateUserProfile == nil {
		f.t.Fatal("unexpected call to UpdateUserProfile")
	}
	return f.updateUserProfile(ctx, arg)
}

func (f *fakeStore) CreateRefreshToken(ctx context.Context, arg store.CreateRefreshTokenParams) error {
	f.t.Helper()
	if f.createRefreshToken == nil {
		f.t.Fatal("unexpected call to CreateRefreshToken")
	}
	return f.createRefreshToken(ctx, arg)
}

func (f *fakeStore) RotateRefreshToken(ctx context.Context, tokenHash []byte) (string, error) {
	f.t.Helper()
	if f.rotateRefreshToken == nil {
		f.t.Fatal("unexpected call to RotateRefreshToken")
	}
	return f.rotateRefreshToken(ctx, tokenHash)
}

func (f *fakeStore) GetRefreshToken(ctx context.Context, tokenHash []byte) (store.RefreshToken, error) {
	f.t.Helper()
	if f.getRefreshToken == nil {
		f.t.Fatal("unexpected call to GetRefreshToken")
	}
	return f.getRefreshToken(ctx, tokenHash)
}

func (f *fakeStore) RevokeRefreshToken(ctx context.Context, tokenHash []byte) error {
	f.t.Helper()
	if f.revokeRefreshToken == nil {
		f.t.Fatal("unexpected call to RevokeRefreshToken")
	}
	return f.revokeRefreshToken(ctx, tokenHash)
}

func (f *fakeStore) RevokeAllRefreshTokens(ctx context.Context, userID string) error {
	f.t.Helper()
	if f.revokeAllRefreshTokens == nil {
		f.t.Fatal("unexpected call to RevokeAllRefreshTokens")
	}
	return f.revokeAllRefreshTokens(ctx, userID)
}

// InTx runs fn against the fake itself and records whether a real database
// would have committed.
func (f *fakeStore) InTx(_ context.Context, fn func(queries) error) error {
	f.t.Helper()
	f.transactions++
	err := fn(f)
	f.committed = err == nil
	return err
}

func newUnitService(t *testing.T, fake *fakeStore) (Service, *authn.Verifier) {
	t.Helper()
	fake.t = t
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return NewService(fake, token.NewIssuer(priv, accessTTL), refreshTTL), authn.NewVerifier(pub)
}

// storedUser returns a user as the database would, with the password hashed at
// the given cost.
func storedUser(t *testing.T, password string, cost int) store.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	require.NoError(t, err)
	now := time.Now()
	return store.User{
		ID:           "11111111-1111-1111-1111-111111111111",
		Email:        "john.doe@example.com",
		PasswordHash: string(hash),
		FullName:     "John Doe",
		Role:         authn.RoleUser,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

var errBoom = errors.New("boom")

func TestServiceRegister_storesNormalizedInputWithBcryptHash(t *testing.T) {
	var stored store.CreateUserParams
	svc, _ := newUnitService(t, &fakeStore{createUser: func(_ context.Context, arg store.CreateUserParams) (store.User, error) {
		stored = arg
		return store.User{ID: "u-1", Email: arg.Email, FullName: arg.FullName, Phone: arg.Phone, Role: arg.Role}, nil
	}})

	got, err := svc.Register(t.Context(), RegisterRequest{
		Email:    "  John.Doe@Example.COM ",
		Password: testPassword,
		FullName: "  John Doe ",
		Phone:    ptr("+6281234567890"),
	})

	require.NoError(t, err)
	require.Equal(t, "john.doe@example.com", stored.Email)
	require.Equal(t, "John Doe", stored.FullName)
	require.Equal(t, authn.RoleUser, stored.Role)
	require.Equal(t, ptr("+6281234567890"), stored.Phone)
	require.NotEqual(t, testPassword, stored.PasswordHash)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(testPassword)))
	require.Equal(t, RegisterResponse{
		ID: "u-1", Email: "john.doe@example.com", FullName: "John Doe", Phone: ptr("+6281234567890"), Role: authn.RoleUser,
	}, got)
}

func TestServiceRegister_neverAssignsAnotherRole(t *testing.T) {
	var stored store.CreateUserParams
	svc, _ := newUnitService(t, &fakeStore{createUser: func(_ context.Context, arg store.CreateUserParams) (store.User, error) {
		stored = arg
		return store.User{}, nil
	}})

	_, err := svc.Register(t.Context(), validRegister())

	require.NoError(t, err)
	require.Equal(t, authn.RoleUser, stored.Role)
}

func TestServiceRegister_doesNotTouchStoreWhenRequestIsInvalid(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{})
	request := validRegister()
	request.Email = "not-an-email"

	_, err := svc.Register(t.Context(), request)

	require.Equal(t, []string{"email"}, validationFields(t, err))
}

func TestServiceRegister_validatesAfterNormalizing(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{})
	request := validRegister()
	request.FullName = "     "

	_, err := svc.Register(t.Context(), request)

	require.Equal(t, []string{"full_name"}, validationFields(t, err))
}

func TestServiceRegister_returnsErrEmailTakenOnUniqueViolation(t *testing.T) {
	uniqueViolation := &pgconn.PgError{Code: "23505"}
	tests := map[string]error{
		"direct":  uniqueViolation,
		"wrapped": fmt.Errorf("exec: %w", uniqueViolation),
	}

	for name, storeErr := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := newUnitService(t, &fakeStore{createUser: func(context.Context, store.CreateUserParams) (store.User, error) {
				return store.User{}, storeErr
			}})

			_, err := svc.Register(t.Context(), validRegister())

			require.ErrorIs(t, err, ErrEmailTaken)
		})
	}
}

func TestServiceRegister_wrapsOtherStoreErrors(t *testing.T) {
	otherViolation := &pgconn.PgError{Code: "23503"}
	tests := map[string]error{
		"plain error":                 errBoom,
		"other postgres error":        otherViolation,
		"wrapped other postgres code": fmt.Errorf("exec: %w", otherViolation),
	}

	for name, storeErr := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := newUnitService(t, &fakeStore{createUser: func(context.Context, store.CreateUserParams) (store.User, error) {
				return store.User{}, storeErr
			}})

			_, err := svc.Register(t.Context(), validRegister())

			require.ErrorIs(t, err, storeErr)
			require.NotErrorIs(t, err, ErrEmailTaken)
		})
	}
}

func TestServiceLogin_issuesTokenPairForValidCredentials(t *testing.T) {
	user := storedUser(t, testPassword, bcrypt.MinCost)
	var lookedUp string
	var refresh store.CreateRefreshTokenParams
	svc, verifier := newUnitService(t, &fakeStore{
		getUserByEmail: func(_ context.Context, email string) (store.User, error) {
			lookedUp = email
			return user, nil
		},
		createRefreshToken: func(_ context.Context, arg store.CreateRefreshTokenParams) error {
			refresh = arg
			return nil
		},
	})

	got, err := svc.Login(t.Context(), LoginRequest{Email: "  JOHN.DOE@Example.com ", Password: testPassword})

	require.NoError(t, err)
	require.Equal(t, "john.doe@example.com", lookedUp)
	require.Equal(t, "Bearer", got.TokenType)
	require.EqualValues(t, accessTTL.Seconds(), got.ExpiresIn)
	require.EqualValues(t, refreshTTL.Seconds(), got.RefreshExpiresIn)

	identity, err := verifier.Verify(got.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authn.Identity{UserID: user.ID, Role: authn.RoleUser}, identity)

	require.Equal(t, user.ID, refresh.UserID)
	require.Equal(t, token.HashRefreshToken(got.RefreshToken), refresh.TokenHash)
	require.WithinDuration(t, time.Now().Add(refreshTTL), refresh.ExpiresAt, time.Minute)
}

func TestServiceLogin_putsTheUsersRoleInTheAccessToken(t *testing.T) {
	admin := storedUser(t, testPassword, bcrypt.MinCost)
	admin.Role = authn.RoleAdmin
	svc, verifier := newUnitService(t, &fakeStore{
		getUserByEmail:     func(context.Context, string) (store.User, error) { return admin, nil },
		createRefreshToken: func(context.Context, store.CreateRefreshTokenParams) error { return nil },
	})

	got, err := svc.Login(t.Context(), LoginRequest{Email: admin.Email, Password: testPassword})

	require.NoError(t, err)
	identity, err := verifier.Verify(got.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authn.RoleAdmin, identity.Role)
}

func TestServiceLogin_rejectsWrongPasswordWithoutIssuingTokens(t *testing.T) {
	user := storedUser(t, testPassword, bcrypt.MinCost)
	svc, _ := newUnitService(t, &fakeStore{
		getUserByEmail: func(context.Context, string) (store.User, error) { return user, nil },
	})

	_, err := svc.Login(t.Context(), LoginRequest{Email: user.Email, Password: "not the password"})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestServiceLogin_rejectsUnknownEmailWithoutIssuingTokens(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{
		getUserByEmail: func(context.Context, string) (store.User, error) { return store.User{}, pgx.ErrNoRows },
	})

	_, err := svc.Login(t.Context(), LoginRequest{Email: "jane.smith@example.com", Password: testPassword})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestServiceLogin_wrapsStoreFailures(t *testing.T) {
	user := storedUser(t, testPassword, bcrypt.MinCost)
	tests := map[string]*fakeStore{
		"user lookup fails": {
			getUserByEmail: func(context.Context, string) (store.User, error) { return store.User{}, errBoom },
		},
		"refresh token insert fails": {
			getUserByEmail:     func(context.Context, string) (store.User, error) { return user, nil },
			createRefreshToken: func(context.Context, store.CreateRefreshTokenParams) error { return errBoom },
		},
	}

	for name, fake := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := newUnitService(t, fake)

			_, err := svc.Login(t.Context(), LoginRequest{Email: user.Email, Password: testPassword})

			require.ErrorIs(t, err, errBoom)
			require.NotErrorIs(t, err, ErrInvalidCredentials)
		})
	}
}

func TestServiceLogin_doesNotTouchStoreWhenRequestIsInvalid(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{})

	_, err := svc.Login(t.Context(), LoginRequest{})

	require.ElementsMatch(t, []string{"email", "password"}, validationFields(t, err))
}

// An unknown email must cost about as much as a wrong password, otherwise the
// response time reveals which emails are registered.
func TestServiceLogin_unknownEmailCostsAsMuchAsWrongPassword(t *testing.T) {
	user := storedUser(t, testPassword, bcrypt.DefaultCost)
	svc, _ := newUnitService(t, &fakeStore{
		getUserByEmail: func(_ context.Context, email string) (store.User, error) {
			if email == user.Email {
				return user, nil
			}
			return store.User{}, pgx.ErrNoRows
		},
	})

	fastest := func(email string) time.Duration {
		best := time.Duration(1<<63 - 1)
		for range 3 {
			start := time.Now()
			_, err := svc.Login(t.Context(), LoginRequest{Email: email, Password: "not the password"})
			require.ErrorIs(t, err, ErrInvalidCredentials)
			best = min(best, time.Since(start))
		}
		return best
	}

	wrongPassword := fastest(user.Email)
	unknownEmail := fastest("jane.smith@example.com")

	require.True(t, unknownEmail > wrongPassword/2,
		"unknown email took %s, wrong password took %s", unknownEmail, wrongPassword)
}

func TestServiceGetProfile_returnsTheStoredUser(t *testing.T) {
	user := storedUser(t, testPassword, bcrypt.MinCost)
	user.Phone = ptr("+6281234567890")
	var askedFor string
	svc, _ := newUnitService(t, &fakeStore{getUserByID: func(_ context.Context, id string) (store.User, error) {
		askedFor = id
		return user, nil
	}})

	got, err := svc.GetProfile(t.Context(), user.ID)

	require.NoError(t, err)
	require.Equal(t, user.ID, askedFor)
	require.Equal(t, ProfileResponse{
		ID: user.ID, Email: user.Email, FullName: user.FullName, Phone: user.Phone, Role: user.Role,
		CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}, got)
}

func TestServiceGetProfile_mapsMissingRowToErrUserNotFound(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{getUserByID: func(context.Context, string) (store.User, error) {
		return store.User{}, pgx.ErrNoRows
	}})

	_, err := svc.GetProfile(t.Context(), "11111111-1111-1111-1111-111111111111")

	require.ErrorIs(t, err, ErrUserNotFound)
}

func TestServiceGetProfile_wrapsOtherStoreErrors(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{getUserByID: func(context.Context, string) (store.User, error) {
		return store.User{}, errBoom
	}})

	_, err := svc.GetProfile(t.Context(), "11111111-1111-1111-1111-111111111111")

	require.ErrorIs(t, err, errBoom)
	require.NotErrorIs(t, err, ErrUserNotFound)
}

func TestServiceUpdateProfile_translatesRequestToStoreParams(t *testing.T) {
	tests := []struct {
		name    string
		request UpdateProfileRequest
		want    store.UpdateUserProfileParams
	}{
		{
			"empty request",
			UpdateProfileRequest{},
			store.UpdateUserProfileParams{ID: "u-1"},
		},
		{
			"full name is trimmed",
			UpdateProfileRequest{FullName: ptr("  Jane Smith ")},
			store.UpdateUserProfileParams{ID: "u-1", FullName: ptr("Jane Smith")},
		},
		{
			"phone value",
			UpdateProfileRequest{Phone: NullableString{Set: true, Value: ptr("08123456789")}},
			store.UpdateUserProfileParams{ID: "u-1", SetPhone: true, Phone: ptr("08123456789")},
		},
		{
			"phone cleared",
			UpdateProfileRequest{Phone: NullableString{Set: true}},
			store.UpdateUserProfileParams{ID: "u-1", SetPhone: true},
		},
		{
			"name and phone together",
			UpdateProfileRequest{FullName: ptr("Jane Smith"), Phone: NullableString{Set: true, Value: ptr("08123456789")}},
			store.UpdateUserProfileParams{ID: "u-1", FullName: ptr("Jane Smith"), SetPhone: true, Phone: ptr("08123456789")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got store.UpdateUserProfileParams
			svc, _ := newUnitService(t, &fakeStore{updateUserProfile: func(_ context.Context, arg store.UpdateUserProfileParams) (store.User, error) {
				got = arg
				return store.User{ID: arg.ID}, nil
			}})

			_, err := svc.UpdateProfile(t.Context(), "u-1", tt.request)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestServiceUpdateProfile_doesNotTouchStoreWhenRequestIsInvalid(t *testing.T) {
	tests := []struct {
		name    string
		request UpdateProfileRequest
		want    []string
	}{
		{"blank full name", UpdateProfileRequest{FullName: ptr("   ")}, []string{"full_name"}},
		{"invalid phone", UpdateProfileRequest{Phone: NullableString{Set: true, Value: ptr("12345")}}, []string{"phone"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newUnitService(t, &fakeStore{})

			_, err := svc.UpdateProfile(t.Context(), "u-1", tt.request)

			require.Equal(t, tt.want, validationFields(t, err))
		})
	}
}

func TestServiceUpdateProfile_mapsMissingRowToErrUserNotFound(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{updateUserProfile: func(context.Context, store.UpdateUserProfileParams) (store.User, error) {
		return store.User{}, pgx.ErrNoRows
	}})

	_, err := svc.UpdateProfile(t.Context(), "u-1", UpdateProfileRequest{FullName: ptr("Jane Smith")})

	require.ErrorIs(t, err, ErrUserNotFound)
}

func TestServiceUpdateProfile_wrapsOtherStoreErrors(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{updateUserProfile: func(context.Context, store.UpdateUserProfileParams) (store.User, error) {
		return store.User{}, errBoom
	}})

	_, err := svc.UpdateProfile(t.Context(), "u-1", UpdateProfileRequest{FullName: ptr("Jane Smith")})

	require.ErrorIs(t, err, errBoom)
	require.NotErrorIs(t, err, ErrUserNotFound)
}

// Login compares against dummyPasswordHash for unknown emails, so it must be a
// valid hash with the same cost as the hashes Register creates.
func TestDummyPasswordHash_isValidAndUsesDefaultCost(t *testing.T) {
	cost, err := bcrypt.Cost([]byte(dummyPasswordHash))

	require.NoError(t, err)
	require.Equal(t, bcrypt.DefaultCost, cost)
}

const oldRefreshToken = "old-refresh-token"

func TestServiceRefresh_rotatesTokenAndIssuesNewPairWithCurrentRole(t *testing.T) {
	user := storedUser(t, testPassword, bcrypt.MinCost)
	user.Role = authn.RoleAdmin
	var rotated []byte
	var issued store.CreateRefreshTokenParams
	fake := &fakeStore{
		rotateRefreshToken: func(_ context.Context, hash []byte) (string, error) {
			rotated = hash
			return user.ID, nil
		},
		getUserByID: func(context.Context, string) (store.User, error) { return user, nil },
		createRefreshToken: func(_ context.Context, arg store.CreateRefreshTokenParams) error {
			issued = arg
			return nil
		},
	}
	svc, verifier := newUnitService(t, fake)

	got, err := svc.Refresh(t.Context(), RefreshRequest{RefreshToken: oldRefreshToken})

	require.NoError(t, err)
	require.True(t, fake.committed)
	require.Equal(t, token.HashRefreshToken(oldRefreshToken), rotated)
	require.NotEqual(t, oldRefreshToken, got.RefreshToken)
	require.EqualValues(t, accessTTL.Seconds(), got.ExpiresIn)
	require.EqualValues(t, refreshTTL.Seconds(), got.RefreshExpiresIn)

	identity, err := verifier.Verify(got.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authn.Identity{UserID: user.ID, Role: authn.RoleAdmin}, identity)

	require.Equal(t, user.ID, issued.UserID)
	require.Equal(t, token.HashRefreshToken(got.RefreshToken), issued.TokenHash)
	require.WithinDuration(t, time.Now().Add(refreshTTL), issued.ExpiresAt, time.Minute)
}

func TestServiceRefresh_rejectsUnknownAndExpiredTokensWithoutRevokingSessions(t *testing.T) {
	tests := map[string]func(context.Context, []byte) (store.RefreshToken, error){
		"unknown token": func(context.Context, []byte) (store.RefreshToken, error) {
			return store.RefreshToken{}, pgx.ErrNoRows
		},
		"expired token": func(context.Context, []byte) (store.RefreshToken, error) {
			return store.RefreshToken{UserID: "u-1", ExpiresAt: time.Now().Add(-time.Hour)}, nil
		},
	}

	for name, lookup := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := newUnitService(t, &fakeStore{
				rotateRefreshToken: func(context.Context, []byte) (string, error) { return "", pgx.ErrNoRows },
				getRefreshToken:    lookup,
			})

			_, err := svc.Refresh(t.Context(), RefreshRequest{RefreshToken: oldRefreshToken})

			require.ErrorIs(t, err, ErrInvalidRefreshToken)
		})
	}
}

func TestServiceRefresh_revokesEverySessionWhenARevokedTokenIsReused(t *testing.T) {
	revokedAt := time.Now().Add(-time.Minute)
	var revokedFor string
	fake := &fakeStore{
		rotateRefreshToken: func(context.Context, []byte) (string, error) { return "", pgx.ErrNoRows },
		getRefreshToken: func(context.Context, []byte) (store.RefreshToken, error) {
			return store.RefreshToken{UserID: "u-1", RevokedAt: &revokedAt, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		revokeAllRefreshTokens: func(_ context.Context, userID string) error {
			revokedFor = userID
			return nil
		},
	}
	svc, _ := newUnitService(t, fake)

	_, err := svc.Refresh(t.Context(), RefreshRequest{RefreshToken: oldRefreshToken})

	require.ErrorIs(t, err, ErrInvalidRefreshToken)
	require.Equal(t, "u-1", revokedFor)
	// The call fails, but the revocation has to be kept.
	require.True(t, fake.committed)
}

func TestServiceRefresh_doesNotConsumeTheTokenWhenAStepFails(t *testing.T) {
	user := storedUser(t, testPassword, bcrypt.MinCost)
	revokedAt := time.Now().Add(-time.Minute)
	rotateOK := func(context.Context, []byte) (string, error) { return user.ID, nil }
	rotateRejected := func(context.Context, []byte) (string, error) { return "", pgx.ErrNoRows }
	reusedToken := func(context.Context, []byte) (store.RefreshToken, error) {
		return store.RefreshToken{UserID: user.ID, RevokedAt: &revokedAt}, nil
	}

	tests := map[string]*fakeStore{
		"rotation fails": {
			rotateRefreshToken: func(context.Context, []byte) (string, error) { return "", errBoom },
		},
		"token lookup fails": {
			rotateRefreshToken: rotateRejected,
			getRefreshToken:    func(context.Context, []byte) (store.RefreshToken, error) { return store.RefreshToken{}, errBoom },
		},
		"revoking all sessions fails": {
			rotateRefreshToken:     rotateRejected,
			getRefreshToken:        reusedToken,
			revokeAllRefreshTokens: func(context.Context, string) error { return errBoom },
		},
		"user lookup fails": {
			rotateRefreshToken: rotateOK,
			getUserByID:        func(context.Context, string) (store.User, error) { return store.User{}, errBoom },
		},
		"new token insert fails": {
			rotateRefreshToken: rotateOK,
			getUserByID:        func(context.Context, string) (store.User, error) { return user, nil },
			createRefreshToken: func(context.Context, store.CreateRefreshTokenParams) error { return errBoom },
		},
	}

	for name, fake := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := newUnitService(t, fake)

			_, err := svc.Refresh(t.Context(), RefreshRequest{RefreshToken: oldRefreshToken})

			require.ErrorIs(t, err, errBoom)
			require.NotErrorIs(t, err, ErrInvalidRefreshToken)
			require.False(t, fake.committed)
		})
	}
}

func TestServiceRefresh_doesNotTouchStoreWhenRequestIsInvalid(t *testing.T) {
	fake := &fakeStore{}
	svc, _ := newUnitService(t, fake)

	_, err := svc.Refresh(t.Context(), RefreshRequest{})

	require.Equal(t, []string{"refresh_token"}, validationFields(t, err))
	require.Zero(t, fake.transactions)
}

func TestServiceLogout_revokesTheTokenByHash(t *testing.T) {
	var revoked []byte
	svc, _ := newUnitService(t, &fakeStore{revokeRefreshToken: func(_ context.Context, hash []byte) error {
		revoked = hash
		return nil
	}})

	err := svc.Logout(t.Context(), LogoutRequest{RefreshToken: oldRefreshToken})

	require.NoError(t, err)
	require.Equal(t, token.HashRefreshToken(oldRefreshToken), revoked)
}

func TestServiceLogout_wrapsStoreFailure(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{revokeRefreshToken: func(context.Context, []byte) error { return errBoom }})

	err := svc.Logout(t.Context(), LogoutRequest{RefreshToken: oldRefreshToken})

	require.ErrorIs(t, err, errBoom)
}

func TestServiceLogout_doesNotTouchStoreWhenRequestIsInvalid(t *testing.T) {
	svc, _ := newUnitService(t, &fakeStore{})

	err := svc.Logout(t.Context(), LogoutRequest{})

	require.Equal(t, []string{"refresh_token"}, validationFields(t, err))
}

// Integration tests below run against a real PostgreSQL container and are
// skipped by `go test -short`.

const unknownUserID = "00000000-0000-0000-0000-000000000000"

func (e *env) registerUser(t *testing.T, request RegisterRequest) RegisterResponse {
	t.Helper()
	user, err := e.service.Register(t.Context(), request)
	require.NoError(t, err)
	return user
}

func (e *env) countRefreshTokens(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, e.pool.QueryRow(t.Context(), "SELECT count(*) FROM refresh_tokens").Scan(&n))
	return n
}

func TestServiceRegister_storesBcryptHashOfPassword(t *testing.T) {
	e := newEnv(t)

	user := e.registerUser(t, validRegister())

	var hash string
	err := e.pool.QueryRow(t.Context(), "SELECT password_hash FROM users WHERE id = $1", user.ID).Scan(&hash)
	require.NoError(t, err)
	require.NotEqual(t, testPassword, hash)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte(testPassword)))
}

func TestServiceRegister_returnsErrEmailTakenIgnoringCase(t *testing.T) {
	e := newEnv(t)
	e.registerUser(t, validRegister())
	duplicate := validRegister()
	duplicate.Email = "JOHN.DOE@example.com"

	_, err := e.service.Register(t.Context(), duplicate)

	require.ErrorIs(t, err, ErrEmailTaken)
	require.Equal(t, 1, e.countUsers(t))
}

func TestServiceLogin_issuesTokenPairAndStoresRefreshHash(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())

	got, err := e.service.Login(t.Context(), LoginRequest{Email: "  JOHN.DOE@example.com ", Password: testPassword})

	require.NoError(t, err)
	require.Equal(t, "Bearer", got.TokenType)
	require.EqualValues(t, accessTTL.Seconds(), got.ExpiresIn)
	require.EqualValues(t, refreshTTL.Seconds(), got.RefreshExpiresIn)

	identity, err := e.verifier.Verify(got.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authn.Identity{UserID: user.ID, Role: authn.RoleUser}, identity)

	var (
		userID    string
		active    bool
		expiresAt time.Time
	)
	err = e.pool.QueryRow(t.Context(),
		"SELECT user_id, revoked_at IS NULL, expires_at FROM refresh_tokens WHERE token_hash = $1",
		token.HashRefreshToken(got.RefreshToken),
	).Scan(&userID, &active, &expiresAt)
	require.NoError(t, err)
	require.Equal(t, user.ID, userID)
	require.True(t, active)
	require.WithinDuration(t, time.Now().Add(refreshTTL), expiresAt, time.Minute)
}

func TestServiceGetProfile_returnsUser(t *testing.T) {
	e := newEnv(t)
	request := validRegister()
	request.Phone = ptr("+6281234567890")
	user := e.registerUser(t, request)

	got, err := e.service.GetProfile(t.Context(), user.ID)

	require.NoError(t, err)
	require.Equal(t, user.ID, got.ID)
	require.Equal(t, user.Email, got.Email)
	require.Equal(t, user.FullName, got.FullName)
	require.Equal(t, user.Phone, got.Phone)
}

func TestServiceGetProfile_returnsErrUserNotFound(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	_, err := e.pool.Exec(t.Context(), "DELETE FROM users WHERE id = $1", user.ID)
	require.NoError(t, err)

	_, deleted := e.service.GetProfile(t.Context(), user.ID)
	_, never := e.service.GetProfile(t.Context(), unknownUserID)

	require.ErrorIs(t, deleted, ErrUserNotFound)
	require.ErrorIs(t, never, ErrUserNotFound)
}

func TestServiceUpdateProfile_changesOnlyFullName(t *testing.T) {
	e := newEnv(t)
	request := validRegister()
	request.Phone = ptr("+6281234567890")
	user := e.registerUser(t, request)

	got, err := e.service.UpdateProfile(t.Context(), user.ID, UpdateProfileRequest{FullName: ptr("  Jane Smith ")})

	require.NoError(t, err)
	require.Equal(t, "Jane Smith", got.FullName)
	require.Equal(t, user.Phone, got.Phone)
	require.True(t, got.UpdatedAt.After(user.UpdatedAt))
}

func TestServiceUpdateProfile_setsPhoneAndKeepsName(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())

	got, err := e.service.UpdateProfile(t.Context(), user.ID, UpdateProfileRequest{
		Phone: NullableString{Set: true, Value: ptr("08123456789")},
	})

	require.NoError(t, err)
	require.Equal(t, "John Doe", got.FullName)
	require.NotNil(t, got.Phone)
	require.Equal(t, "08123456789", *got.Phone)
}

func TestServiceUpdateProfile_clearsPhoneWhenExplicitlyNull(t *testing.T) {
	e := newEnv(t)
	request := validRegister()
	request.Phone = ptr("+6281234567890")
	user := e.registerUser(t, request)

	got, err := e.service.UpdateProfile(t.Context(), user.ID, UpdateProfileRequest{
		Phone: NullableString{Set: true},
	})

	require.NoError(t, err)
	require.Nil(t, got.Phone)
}

func TestServiceUpdateProfile_emptyRequestChangesNothing(t *testing.T) {
	e := newEnv(t)
	request := validRegister()
	request.Phone = ptr("+6281234567890")
	user := e.registerUser(t, request)

	got, err := e.service.UpdateProfile(t.Context(), user.ID, UpdateProfileRequest{})

	require.NoError(t, err)
	require.Equal(t, user.FullName, got.FullName)
	require.Equal(t, user.Phone, got.Phone)
}

func TestServiceUpdateProfile_returnsErrUserNotFound(t *testing.T) {
	e := newEnv(t)

	_, err := e.service.UpdateProfile(t.Context(), unknownUserID, UpdateProfileRequest{FullName: ptr("Jane Smith")})

	require.ErrorIs(t, err, ErrUserNotFound)
}

func TestServiceLogin_storesOneRefreshTokenPerLogin(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	request := LoginRequest{Email: user.Email, Password: testPassword}

	first, err := e.service.Login(t.Context(), request)
	require.NoError(t, err)
	second, err := e.service.Login(t.Context(), request)
	require.NoError(t, err)

	require.NotEqual(t, first.RefreshToken, second.RefreshToken)
	require.Equal(t, 2, e.countRefreshTokens(t))
}

func (e *env) openSession(t *testing.T, user RegisterResponse) LoginResponse {
	t.Helper()
	got, err := e.service.Login(t.Context(), LoginRequest{Email: user.Email, Password: testPassword})
	require.NoError(t, err)
	return got
}

func (e *env) activeRefreshTokens(t *testing.T, userID string) int {
	t.Helper()
	var n int
	err := e.pool.QueryRow(t.Context(),
		"SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL", userID).Scan(&n)
	require.NoError(t, err)
	return n
}

func TestServiceRefresh_rotatesTokenInDatabase(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	first := e.openSession(t, user)

	got, err := e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: first.RefreshToken})

	require.NoError(t, err)
	require.NotEqual(t, first.RefreshToken, got.RefreshToken)
	identity, err := e.verifier.Verify(got.AccessToken)
	require.NoError(t, err)
	require.Equal(t, user.ID, identity.UserID)

	var revoked bool
	err = e.pool.QueryRow(t.Context(), "SELECT revoked_at IS NOT NULL FROM refresh_tokens WHERE token_hash = $1",
		token.HashRefreshToken(first.RefreshToken)).Scan(&revoked)
	require.NoError(t, err)
	require.True(t, revoked)

	var (
		active    bool
		expiresAt time.Time
	)
	err = e.pool.QueryRow(t.Context(), "SELECT revoked_at IS NULL, expires_at FROM refresh_tokens WHERE token_hash = $1",
		token.HashRefreshToken(got.RefreshToken)).Scan(&active, &expiresAt)
	require.NoError(t, err)
	require.True(t, active)
	require.WithinDuration(t, time.Now().Add(refreshTTL), expiresAt, time.Minute)
}

func TestServiceRefresh_revokesEverySessionInDatabaseWhenARevokedTokenIsReused(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	first := e.openSession(t, user)
	otherDevice := e.openSession(t, user)
	rotated, err := e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: first.RefreshToken})
	require.NoError(t, err)

	_, err = e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: first.RefreshToken})

	require.ErrorIs(t, err, ErrInvalidRefreshToken)
	require.Zero(t, e.activeRefreshTokens(t, user.ID))
	for _, session := range []string{rotated.RefreshToken, otherDevice.RefreshToken} {
		_, err = e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: session})
		require.ErrorIs(t, err, ErrInvalidRefreshToken)
	}
}

func TestServiceRefresh_rejectsExpiredTokenWithoutRevokingOtherSessions(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	expired := e.openSession(t, user)
	other := e.openSession(t, user)
	_, err := e.pool.Exec(t.Context(), "UPDATE refresh_tokens SET expires_at = now() - interval '1 minute' WHERE token_hash = $1",
		token.HashRefreshToken(expired.RefreshToken))
	require.NoError(t, err)

	_, err = e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: expired.RefreshToken})

	require.ErrorIs(t, err, ErrInvalidRefreshToken)
	require.Equal(t, 2, e.activeRefreshTokens(t, user.ID))
	_, err = e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: other.RefreshToken})
	require.NoError(t, err)
}

func TestServiceRefresh_rejectsUnknownTokenWithoutRevokingSessions(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	e.openSession(t, user)

	_, err := e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: "never-issued"})

	require.ErrorIs(t, err, ErrInvalidRefreshToken)
	require.Equal(t, 1, e.activeRefreshTokens(t, user.ID))
}

func TestServiceRefresh_readsTheCurrentRoleFromTheDatabase(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	session := e.openSession(t, user)
	_, err := e.pool.Exec(t.Context(), "UPDATE users SET role = 'admin' WHERE id = $1", user.ID)
	require.NoError(t, err)

	got, err := e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: session.RefreshToken})

	require.NoError(t, err)
	identity, err := e.verifier.Verify(got.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authn.RoleAdmin, identity.Role)
}

func TestServiceRefresh_allowsOnlyOneUseOfATokenWhenCalledConcurrently(t *testing.T) {
	const attempts = 5
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	session := e.openSession(t, user)

	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: session.RefreshToken})
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, ErrInvalidRefreshToken)
	}
	require.Equal(t, 1, succeeded)
	// Every losing attempt counts as reuse, so nothing stays active.
	require.Zero(t, e.activeRefreshTokens(t, user.ID))
}

func TestServiceLogout_revokesTheTokenAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	session := e.openSession(t, user)
	request := LogoutRequest{RefreshToken: session.RefreshToken}

	require.NoError(t, e.service.Logout(t.Context(), request))
	require.NoError(t, e.service.Logout(t.Context(), request))

	require.Zero(t, e.activeRefreshTokens(t, user.ID))
	_, err := e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: session.RefreshToken})
	require.ErrorIs(t, err, ErrInvalidRefreshToken)
}

func TestServiceLogout_onlyEndsTheGivenSession(t *testing.T) {
	e := newEnv(t)
	user := e.registerUser(t, validRegister())
	first := e.openSession(t, user)
	second := e.openSession(t, user)

	require.NoError(t, e.service.Logout(t.Context(), LogoutRequest{RefreshToken: first.RefreshToken}))

	require.Equal(t, 1, e.activeRefreshTokens(t, user.ID))
	_, err := e.service.Refresh(t.Context(), RefreshRequest{RefreshToken: second.RefreshToken})
	require.NoError(t, err)
}

func TestServiceLogout_acceptsUnknownToken(t *testing.T) {
	e := newEnv(t)

	err := e.service.Logout(t.Context(), LogoutRequest{RefreshToken: "never-issued"})

	require.NoError(t, err)
}
