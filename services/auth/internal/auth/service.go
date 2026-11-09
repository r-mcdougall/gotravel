package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/services/auth/internal/store"
	"github.com/r-mcdougall/gotravel/services/auth/internal/token"
)

const uniqueViolationCode = "23505"
const dummyPasswordHash = "$2a$10$1txCLvw1vUn8cpi/fIwSxOvsR31qeHqDYM59DnpY7HkCkAdbfpyxe" //nolint:gosec // G101 false positive: a bcrypt hash of a throwaway value, only used to equalize login timing

type queries interface {
	CreateUser(ctx context.Context, arg store.CreateUserParams) (store.User, error)
	GetUserByEmail(ctx context.Context, email string) (store.User, error)
	GetUserByID(ctx context.Context, id string) (store.User, error)
	UpdateUserProfile(ctx context.Context, arg store.UpdateUserProfileParams) (store.User, error)
	CreateRefreshToken(ctx context.Context, arg store.CreateRefreshTokenParams) error
	RotateRefreshToken(ctx context.Context, tokenHash []byte) (string, error)
	GetRefreshToken(ctx context.Context, tokenHash []byte) (store.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, tokenHash []byte) error
	RevokeAllRefreshTokens(ctx context.Context, userID string) error
}

var _ queries = (*store.Queries)(nil)

type authStore interface {
	queries
	InTx(ctx context.Context, fn func(queries) error) error
}

type service struct {
	db         authStore
	issuer     *token.Issuer
	refreshTTL time.Duration
}

func NewService(db authStore, issuer *token.Issuer, refreshTTL time.Duration) Service {
	return &service{
		db:         db,
		issuer:     issuer,
		refreshTTL: refreshTTL,
	}
}

func (s *service) Register(ctx context.Context, request RegisterRequest) (RegisterResponse, error) {
	request.normalize()

	if err := request.Validate(); err != nil {
		return RegisterResponse{}, fmt.Errorf("validate request: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return RegisterResponse{}, fmt.Errorf("hash password: %w", err)
	}

	params := store.CreateUserParams{
		Email:        request.Email,
		PasswordHash: string(hash),
		FullName:     request.FullName,
		Phone:        request.Phone,
		Role:         authn.RoleUser,
	}

	user, err := s.db.CreateUser(ctx, params)
	if err != nil {
		if isUniqueViolation(err) {
			return RegisterResponse{}, ErrEmailTaken
		}
		return RegisterResponse{}, fmt.Errorf("create user: %w", err)
	}

	return toUserResponse(user), nil
}

func (s *service) Login(ctx context.Context, request LoginRequest) (LoginResponse, error) {
	request.normalize()

	if err := request.Validate(); err != nil {
		return LoginResponse{}, fmt.Errorf("validate request: %w", err)
	}

	user, err := s.db.GetUserByEmail(ctx, request.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(request.Password))
		return LoginResponse{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResponse{}, fmt.Errorf("get user by email: %w", err)
	}

	if err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.Password)); err != nil {
		return LoginResponse{}, ErrInvalidCredentials
	}

	pair, err := s.issueTokenPair(ctx, s.db, user)
	if err != nil {
		return LoginResponse{}, fmt.Errorf("issue token pair: %w", err)
	}

	return toTokenPairResponse(pair), nil
}

func (s *service) Refresh(ctx context.Context, request RefreshRequest) (RefreshResponse, error) {
	if err := request.Validate(); err != nil {
		return RefreshResponse{}, fmt.Errorf("validate request: %w", err)
	}

	var (
		pair     TokenPair
		rejected bool
	)

	fn := func(q queries) error {
		hash := token.HashRefreshToken(request.RefreshToken)

		userID, err := q.RotateRefreshToken(ctx, hash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				rejected = true
				return revokeSessionsIfReused(ctx, q, hash)
			}
			return fmt.Errorf("rotate refresh token: %w", err)
		}

		user, err := q.GetUserByID(ctx, userID)
		if err != nil {
			return fmt.Errorf("get user by id: %w", err)
		}

		pair, err = s.issueTokenPair(ctx, q, user)
		if err != nil {
			return fmt.Errorf("issue token pair: %w", err)
		}

		return nil
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return RefreshResponse{}, err
	}
	if rejected {
		return RefreshResponse{}, ErrInvalidRefreshToken
	}

	return toTokenPairResponse(pair), nil
}

func (s *service) Logout(ctx context.Context, request LogoutRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate request: %w", err)
	}

	if err := s.db.RevokeRefreshToken(ctx, token.HashRefreshToken(request.RefreshToken)); err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}

	return nil
}

func (s *service) GetProfile(ctx context.Context, userID string) (ProfileResponse, error) {
	user, err := s.db.GetUserByID(ctx, userID)
	if err != nil {
		return ProfileResponse{}, userQueryError("get user by id", err)
	}

	return toUserResponse(user), nil
}

func (s *service) UpdateProfile(ctx context.Context, userID string, request UpdateProfileRequest) (ProfileResponse, error) {
	request.normalize()

	if err := request.Validate(); err != nil {
		return ProfileResponse{}, fmt.Errorf("validate request: %w", err)
	}

	params := store.UpdateUserProfileParams{
		ID:       userID,
		FullName: request.FullName,
		SetPhone: request.Phone.Set,
		Phone:    request.Phone.Value,
	}

	user, err := s.db.UpdateUserProfile(ctx, params)
	if err != nil {
		return ProfileResponse{}, userQueryError("update user", err)
	}

	return toUserResponse(user), nil
}

func revokeSessionsIfReused(ctx context.Context, q queries, hash []byte) error {
	row, err := q.GetRefreshToken(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("get refresh token: %w", err)
	}
	if row.RevokedAt == nil {
		return nil
	}

	if err = q.RevokeAllRefreshTokens(ctx, row.UserID); err != nil {
		return fmt.Errorf("revoke all refresh tokens: %w", err)
	}

	return nil
}

func (s *service) issueTokenPair(ctx context.Context, q queries, user store.User) (TokenPair, error) {
	access, err := s.issuer.Issue(user.ID, user.Role)
	if err != nil {
		return TokenPair{}, err
	}

	refresh, refreshHash, err := token.NewRefreshToken()
	if err != nil {
		return TokenPair{}, err
	}

	params := store.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(s.refreshTTL),
	}

	if err = q.CreateRefreshToken(ctx, params); err != nil {
		return TokenPair{}, fmt.Errorf("create refresh token: %w", err)
	}

	return TokenPair{
		AccessToken:  access,
		AccessTTL:    s.issuer.TTL(),
		RefreshToken: refresh,
		RefreshTTL:   s.refreshTTL,
	}, nil
}

func toUserResponse(u store.User) UserResponse {
	return UserResponse{
		ID:        u.ID,
		Email:     u.Email,
		FullName:  u.FullName,
		Phone:     u.Phone,
		Role:      u.Role,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

func toTokenPairResponse(p TokenPair) TokenPairResponse {
	return TokenPairResponse{
		AccessToken:      p.AccessToken,
		TokenType:        tokenTypeBearer,
		ExpiresIn:        int64(p.AccessTTL.Seconds()),
		RefreshToken:     p.RefreshToken,
		RefreshExpiresIn: int64(p.RefreshTTL.Seconds()),
	}
}

func userQueryError(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode
}
