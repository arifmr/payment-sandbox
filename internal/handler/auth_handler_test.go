package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/service"
)

func TestRegister_Created(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"name": "Toko A", "email": "toko@example.com", "password": "password123",
	})

	assertStatus(t, rec, http.StatusCreated)

	var body model.UserResponse
	decode(t, rec, &body)
	if body.Email != "toko@example.com" {
		t.Errorf("email = %q", body.Email)
	}
	if body.Role != string(constant.RoleMerchant) {
		t.Errorf("role = %q, want MERCHANT — self-registration must never mint an admin", body.Role)
	}
	if body.ID == "" {
		t.Error("id missing from response")
	}
}

// The response must never echo the password or a hash of it (SRS §5.2).
func TestRegister_ResponseHasNoCredentialFields(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"name": "Toko A", "email": "toko@example.com", "password": "password123",
	})

	assertStatus(t, rec, http.StatusCreated)
	var raw map[string]any
	decode(t, rec, &raw)
	for _, forbidden := range []string{"password", "password_hash", "PasswordHash"} {
		if _, present := raw[forbidden]; present {
			t.Errorf("response exposes %q", forbidden)
		}
	}
	if body := rec.Body.String(); strings.Contains(body, "password123") {
		t.Errorf("response echoes the plaintext password: %s", body)
	}
}

// SRS §4.5: email format and required fields are validated with clear errors.
func TestRegister_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		body any
	}{
		{"malformed email", map[string]any{"name": "Toko", "email": "not-an-email", "password": "password123"}},
		{"missing email", map[string]any{"name": "Toko", "password": "password123"}},
		{"missing name", map[string]any{"email": "a@b.com", "password": "password123"}},
		{"name too short", map[string]any{"name": "A", "email": "a@b.com", "password": "password123"}},
		{"password too short", map[string]any{"name": "Toko", "email": "a@b.com", "password": "short"}},
		{"password beyond bcrypt limit", map[string]any{"name": "Toko", "email": "a@b.com", "password": strings.Repeat("x", 73)}},
		{"empty object", map[string]any{}},
		{"malformed json", `{"name":`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv()
			rec := e.do(t, http.MethodPost, "/api/v1/auth/register", "", tc.body)

			assertStatus(t, rec, http.StatusBadRequest)
			if code := errCode(t, rec); code == "" {
				t.Error("error response must carry a code")
			}
		})
	}
}

func TestRegister_DuplicateEmailIsConflict(t *testing.T) {
	e := newTestEnv()
	e.auth.registerFn = func(context.Context, string, string, string) (*model.User, error) {
		return nil, apperror.ErrEmailTaken
	}

	rec := e.do(t, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"name": "Toko A", "email": "toko@example.com", "password": "password123",
	})

	assertStatus(t, rec, http.StatusConflict)
	if code := errCode(t, rec); code != "EMAIL_TAKEN" {
		t.Errorf("error code = %q, want EMAIL_TAKEN", code)
	}
}

func TestLogin_ReturnsBothTokens(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "toko@example.com", "password": "password123",
	})

	assertStatus(t, rec, http.StatusOK)

	var body model.LoginResponse
	decode(t, rec, &body)
	if body.AccessToken == "" || body.RefreshToken == "" {
		t.Fatalf("both tokens must be returned: %+v", body)
	}
	if body.AccessExpiresAt.IsZero() {
		t.Error("access_expires_at must be set so the client can refresh in time")
	}
	if body.User.Email != "toko@example.com" {
		t.Errorf("user.email = %q", body.User.Email)
	}
	if e.auth.lastPassword != "password123" {
		t.Errorf("handler passed password %q to the service", e.auth.lastPassword)
	}
}

func TestLogin_WrongCredentialsIsUnauthorized(t *testing.T) {
	e := newTestEnv()
	e.auth.loginFn = func(context.Context, string, string) (*service.TokenPair, error) {
		return nil, apperror.ErrInvalidCredentials
	}

	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "toko@example.com", "password": "wrong-password",
	})

	assertStatus(t, rec, http.StatusUnauthorized)
	if code := errCode(t, rec); code != "INVALID_CREDENTIALS" {
		t.Errorf("error code = %q, want INVALID_CREDENTIALS", code)
	}
	// The message must not reveal whether the account exists.
	if body := rec.Body.String(); strings.Contains(body, "not found") || strings.Contains(body, "no such user") {
		t.Errorf("login error discloses account existence: %s", body)
	}
}

func TestLogin_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		body any
	}{
		{"missing password", map[string]any{"email": "a@b.com"}},
		{"missing email", map[string]any{"password": "password123"}},
		{"malformed email", map[string]any{"email": "nope", "password": "password123"}},
		{"empty body", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv()
			rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", tc.body)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestRefresh_RotatesTokens(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{
		"refresh_token": "some-refresh-token",
	})

	assertStatus(t, rec, http.StatusOK)
	var body model.LoginResponse
	decode(t, rec, &body)
	if body.AccessToken == "" || body.RefreshToken == "" {
		t.Error("refresh must return a new token pair")
	}
}

func TestRefresh_RevokedTokenIsUnauthorized(t *testing.T) {
	e := newTestEnv()
	e.auth.refreshFn = func(context.Context, string) (*service.TokenPair, error) {
		return nil, apperror.ErrUnauthorized
	}

	rec := e.do(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{
		"refresh_token": "revoked",
	})

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestRefresh_MissingTokenIsBadRequest(t *testing.T) {
	e := newTestEnv()
	rec := e.do(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{})
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestLogout_NoContent(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodPost, "/api/v1/auth/logout", "", map[string]any{
		"refresh_token": "some-refresh-token",
	})

	assertStatus(t, rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Errorf("204 must have an empty body, got %q", rec.Body.String())
	}
	if e.auth.logoutCalls != 1 {
		t.Errorf("Logout called %d times, want 1", e.auth.logoutCalls)
	}
}

// Logout is idempotent: replaying it must not error.
func TestLogout_IsIdempotent(t *testing.T) {
	e := newTestEnv()
	body := map[string]any{"refresh_token": "same-token"}

	for i := 0; i < 2; i++ {
		rec := e.do(t, http.MethodPost, "/api/v1/auth/logout", "", body)
		assertStatus(t, rec, http.StatusNoContent)
	}
	if e.auth.logoutCalls != 2 {
		t.Errorf("Logout called %d times, want 2", e.auth.logoutCalls)
	}
}

func TestHealthz(t *testing.T) {
	e := newTestEnv()
	rec := e.do(t, http.MethodGet, "/healthz", "", nil)
	assertStatus(t, rec, http.StatusOK)
}

// Auth endpoints are public and must not require a bearer token.
func TestAuthEndpointsArePublic(t *testing.T) {
	e := newTestEnv()
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/login", "/api/v1/auth/refresh", "/api/v1/auth/logout"} {
		rec := e.do(t, http.MethodPost, path, "", map[string]any{})
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s returned 401 without a token; it must be public", path)
		}
	}
}

// A token that was not signed by this server must not open a protected route.
func TestProtectedRoute_RejectsForgedToken(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodGet, "/api/v1/wallet", "Bearer forged.token.here", nil)
	assertStatus(t, rec, http.StatusUnauthorized)
}
