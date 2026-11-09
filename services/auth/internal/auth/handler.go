package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

type Service interface {
	Register(ctx context.Context, request RegisterRequest) (RegisterResponse, error)
	Login(ctx context.Context, request LoginRequest) (LoginResponse, error)
	Refresh(ctx context.Context, request RefreshRequest) (RefreshResponse, error)
	Logout(ctx context.Context, request LogoutRequest) error
	GetProfile(ctx context.Context, userID string) (ProfileResponse, error)
	UpdateProfile(ctx context.Context, userID string, request UpdateProfileRequest) (ProfileResponse, error)
}

type Handler struct {
	service  Service
	log      *slog.Logger
	verifier *authn.Verifier
}

func NewHandler(service Service, log *slog.Logger, verifier *authn.Verifier) *Handler {
	return &Handler{
		service:  service,
		log:      log,
		verifier: verifier,
	}
}

func (h *Handler) Routes(r chi.Router) {
	r.Post("/api/v1/auth/register", h.Register)
	r.Post("/api/v1/auth/login", h.Login)
	r.Post("/api/v1/auth/refresh", h.Refresh)
	r.Post("/api/v1/auth/logout", h.Logout)

	r.Group(func(r chi.Router) {
		r.Use(authn.Authenticate(h.verifier))
		r.Get("/api/v1/users/me", h.GetMe)
		r.Patch("/api/v1/users/me", h.UpdateMe)
	})
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var request RegisterRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.Register(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, response)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var request LoginRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.Login(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var request RefreshRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.Refresh(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var request LogoutRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	if err := h.service.Logout(r.Context(), request); err != nil {
		h.writeError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFrom(r.Context())
	if !ok {
		unauthorized(w)
		return
	}

	response, err := h.service.GetProfile(r.Context(), identity.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFrom(r.Context())
	if !ok {
		unauthorized(w)
		return
	}

	var request UpdateProfileRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.UpdateProfile(r.Context(), identity.UserID, request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *ValidationError

	switch {
	case errors.As(err, &validationErr):
		httpserver.WriteValidationError(w, validationErr.Details)
	case errors.Is(err, ErrEmailTaken):
		httpserver.WriteError(w, http.StatusConflict, "EMAIL_TAKEN", "email is already registered")
	case errors.Is(err, ErrInvalidCredentials):
		httpserver.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
	case errors.Is(err, ErrInvalidRefreshToken):
		httpserver.WriteError(w, http.StatusUnauthorized, "INVALID_REFRESH_TOKEN", "refresh token is invalid or expired")
	case errors.Is(err, ErrUserNotFound):
		unauthorized(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "request failed",
		slog.String("request_id", httpserver.RequestIDFrom(r.Context())),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", err),
	)
	httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
}

func invalidBody(w http.ResponseWriter) {
	httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
}

func unauthorized(w http.ResponseWriter) {
	httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid access token")
}
