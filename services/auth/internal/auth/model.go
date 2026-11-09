package auth

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

const tokenTypeBearer = "Bearer"

type User struct {
	ID        string
	Email     string
	FullName  string
	Phone     *string
	Role      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type TokenPair struct {
	AccessToken  string
	AccessTTL    time.Duration
	RefreshToken string
	RefreshTTL   time.Duration
}

type TokenPairResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
}

type RegisterRequest struct {
	Email    string  `json:"email"`
	Password string  `json:"password"`
	FullName string  `json:"full_name"`
	Phone    *string `json:"phone"`
}

func (r *RegisterRequest) normalize() {
	r.Email = normalizeEmail(r.Email)
	r.FullName = strings.TrimSpace(r.FullName)
}

func (r *RegisterRequest) Validate() error {
	var v ValidationError

	v.check(validEmail(r.Email), "email", "must be a valid email address")
	v.check(between(len(r.Password), minPasswordBytes, maxPasswordBytes), "password", "must be between 8 and 72 characters")
	v.check(between(utf8.RuneCountInString(r.FullName), 1, maxFullNameRunes), "full_name", "must be between 1 and 100 characters")
	v.check(r.Phone == nil || validPhone(*r.Phone), "phone", phoneMessage)

	return v.err()
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r *LoginRequest) normalize() {
	r.Email = normalizeEmail(r.Email)
}

func (r *LoginRequest) Validate() error {
	var v ValidationError

	v.check(r.Email != "", "email", "is required")
	v.check(r.Password != "", "password", "is required")

	return v.err()
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (r *RefreshRequest) Validate() error {
	var v ValidationError
	v.check(r.RefreshToken != "", "refresh_token", "is required")
	return v.err()
}

type UpdateProfileRequest struct {
	FullName *string        `json:"full_name"`
	Phone    NullableString `json:"phone"`
}

func (r *UpdateProfileRequest) normalize() {
	if r.FullName != nil {
		trimmed := strings.TrimSpace(*r.FullName)
		r.FullName = &trimmed
	}
}

func (r *UpdateProfileRequest) Validate() error {
	var v ValidationError
	v.check(r.FullName == nil || between(utf8.RuneCountInString(*r.FullName), 1, maxFullNameRunes), "full_name", "must be between 1 and 100 characters")
	v.check(r.Phone.Value == nil || validPhone(*r.Phone.Value), "phone", phoneMessage)
	return v.err()
}

type NullableString struct {
	Set   bool
	Value *string
}

func (n *NullableString) UnmarshalJSON(data []byte) error {
	n.Set = true
	if bytes.Equal(data, []byte("null")) {
		n.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	n.Value = &s
	return nil
}

type UserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Phone     *string   `json:"phone"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type LogoutRequest = RefreshRequest

type RegisterResponse = UserResponse

type LoginResponse = TokenPairResponse

type RefreshResponse = TokenPairResponse

type ProfileResponse = UserResponse

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
