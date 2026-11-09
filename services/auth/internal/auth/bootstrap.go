package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/bcrypt"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"

	"github.com/r-mcdougall/gotravel/services/auth/internal/store"
)

const adminFullName = "Administrator"

type adminStore interface {
	CreateAdminIfAbsent(ctx context.Context, params store.CreateAdminIfAbsentParams) (int64, error)
	GetUserByEmail(ctx context.Context, email string) (store.User, error)
}

var _ adminStore = (*store.Queries)(nil)

func BootstrapAdmin(ctx context.Context, db adminStore, log *slog.Logger, email, password string) error {
	if email == "" && password == "" {
		return nil
	}
	if email == "" || password == "" {
		return errors.New("admin email and password must be set together")
	}

	request := RegisterRequest{Email: email, Password: password, FullName: adminFullName}
	request.normalize()
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate admin credentials: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	params := store.CreateAdminIfAbsentParams{
		Email:        request.Email,
		PasswordHash: string(hash),
		FullName:     request.FullName,
	}

	created, err := db.CreateAdminIfAbsent(ctx, params)
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	if created == 1 {
		log.Info("admin created successfully", "email", request.Email)
		return nil
	}

	existing, err := db.GetUserByEmail(ctx, request.Email)
	if err != nil {
		return fmt.Errorf("get existing user: %w", err)
	}
	if existing.Role != authn.RoleAdmin {
		log.Warn("admin not created: email belongs to a regular user, which is not promoted", "email", request.Email)
		return nil
	}

	log.Info("admin already exists", "email", request.Email)
	return nil
}
