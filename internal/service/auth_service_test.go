package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/pkg/hash"
	pkgjwt "github.com/dboarif/payment-sandbox/internal/pkg/jwt"
	"github.com/dboarif/payment-sandbox/internal/repository"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newTestHasher() *hash.Hasher   { return hash.New(bcrypt.MinCost) }
func newTestJWT() *pkgjwt.Manager   { return pkgjwt.New("test-secret", time.Minute) }

func newTestAuthSvc(u repository.UserRepository, w repository.WalletRepository, rt repository.RefreshTokenRepository) AuthService {
	return NewAuthService(u, w, rt, newTestHasher(), newTestJWT(), noopUoW{}, 7*24*time.Hour)
}

// ── mocks ─────────────────────────────────────────────────────────────────────

type authUserRepo struct {
	byEmail map[string]*model.User
	byID    map[uuid.UUID]*model.User
}

func newAuthUserRepo() *authUserRepo {
	return &authUserRepo{
		byEmail: map[string]*model.User{},
		byID:    map[uuid.UUID]*model.User{},
	}
}

func (r *authUserRepo) Create(_ context.Context, u *model.User) error {
	r.byEmail[u.Email] = u
	r.byID[u.ID] = u
	return nil
}

func (r *authUserRepo) FindByEmail(_ context.Context, email string) (*model.User, error) {
	u, ok := r.byEmail[email]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return u, nil
}

func (r *authUserRepo) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return u, nil
}

type authWalletRepo struct{}

func (authWalletRepo) Create(context.Context, *model.Wallet) error              { return nil }
func (authWalletRepo) FindByMerchantID(context.Context, uuid.UUID) (*model.Wallet, error) {
	return nil, apperror.ErrNotFound
}
func (authWalletRepo) AddBalance(context.Context, uuid.UUID, int64) error { return nil }

type authRefreshRepo struct {
	store map[string]*model.RefreshToken // keyed by hash
}

func newAuthRefreshRepo() *authRefreshRepo {
	return &authRefreshRepo{store: map[string]*model.RefreshToken{}}
}

func (r *authRefreshRepo) Create(_ context.Context, t *model.RefreshToken) error {
	r.store[t.TokenHash] = t
	return nil
}

func (r *authRefreshRepo) FindByHash(_ context.Context, h string) (*model.RefreshToken, error) {
	t, ok := r.store[h]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return t, nil
}

func (r *authRefreshRepo) Revoke(_ context.Context, id uuid.UUID, at time.Time) error {
	for _, t := range r.store {
		if t.ID == id {
			if t.RevokedAt != nil {
				return apperror.ErrInvalidState
			}
			t.RevokedAt = &at
			return nil
		}
	}
	return apperror.ErrNotFound
}

func (r *authRefreshRepo) RevokeAllForUser(_ context.Context, userID uuid.UUID, at time.Time) error {
	for _, t := range r.store {
		if t.UserID == userID && t.RevokedAt == nil {
			t.RevokedAt = &at
		}
	}
	return nil
}

// interface guards
var _ repository.UserRepository         = (*authUserRepo)(nil)
var _ repository.WalletRepository       = (authWalletRepo{})
var _ repository.RefreshTokenRepository = (*authRefreshRepo)(nil)

// ── Register ─────────────────────────────────────────────────────────────────

func TestAuthService_Register_HappyPath(t *testing.T) {
	uRepo := newAuthUserRepo()
	svc := newTestAuthSvc(uRepo, authWalletRepo{}, newAuthRefreshRepo())

	u, err := svc.Register(context.Background(), "Alice", "alice@example.com", "password123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "alice@example.com" {
		t.Fatalf("want email alice@example.com, got %s", u.Email)
	}
	if u.Role != constant.RoleMerchant {
		t.Fatalf("want role MERCHANT, got %s", u.Role)
	}
	if _, ok := uRepo.byEmail["alice@example.com"]; !ok {
		t.Fatal("user not persisted")
	}
}

func TestAuthService_Register_EmailNormalized(t *testing.T) {
	uRepo := newAuthUserRepo()
	svc := newTestAuthSvc(uRepo, authWalletRepo{}, newAuthRefreshRepo())

	u, err := svc.Register(context.Background(), "Bob", "  BOB@Example.COM  ", "password123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "bob@example.com" {
		t.Fatalf("want normalized email, got %s", u.Email)
	}
}

func TestAuthService_Register_MissingFields(t *testing.T) {
	svc := newTestAuthSvc(newAuthUserRepo(), authWalletRepo{}, newAuthRefreshRepo())

	cases := []struct{ name, email, password string }{
		{"", "a@b.com", "pass1234"},
		{"Alice", "", "pass1234"},
		{"Alice", "a@b.com", ""},
	}
	for _, c := range cases {
		_, err := svc.Register(context.Background(), c.name, c.email, c.password)
		if err == nil {
			t.Errorf("Register(%q,%q,%q): want error, got nil", c.name, c.email, c.password)
		}
	}
}

func TestAuthService_Register_EmailTaken(t *testing.T) {
	uRepo := newAuthUserRepo()
	svc := newTestAuthSvc(uRepo, authWalletRepo{}, newAuthRefreshRepo())

	if _, err := svc.Register(context.Background(), "Alice", "alice@example.com", "password123"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, err := svc.Register(context.Background(), "Alice2", "alice@example.com", "password456")
	if !errors.Is(err, apperror.ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}
}

// ── Login ─────────────────────────────────────────────────────────────────────

func TestAuthService_Login_HappyPath(t *testing.T) {
	uRepo := newAuthUserRepo()
	rtRepo := newAuthRefreshRepo()
	svc := newTestAuthSvc(uRepo, authWalletRepo{}, rtRepo)

	if _, err := svc.Register(context.Background(), "Alice", "alice@example.com", "password123"); err != nil {
		t.Fatalf("register: %v", err)
	}

	pair, err := svc.Login(context.Background(), "alice@example.com", "password123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("empty access token")
	}
	if pair.RefreshToken == "" {
		t.Fatal("empty refresh token")
	}
	if pair.User.Email != "alice@example.com" {
		t.Fatalf("wrong user in pair: %s", pair.User.Email)
	}
}

func TestAuthService_Login_UserNotFound(t *testing.T) {
	svc := newTestAuthSvc(newAuthUserRepo(), authWalletRepo{}, newAuthRefreshRepo())

	_, err := svc.Login(context.Background(), "nobody@example.com", "password123")
	if !errors.Is(err, apperror.ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthService_Login_WrongPassword(t *testing.T) {
	svc := newTestAuthSvc(newAuthUserRepo(), authWalletRepo{}, newAuthRefreshRepo())

	if _, err := svc.Register(context.Background(), "Alice", "alice@example.com", "password123"); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := svc.Login(context.Background(), "alice@example.com", "wrongpass")
	if !errors.Is(err, apperror.ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}

// ── Refresh ───────────────────────────────────────────────────────────────────

func TestAuthService_Refresh_HappyPath(t *testing.T) {
	uRepo := newAuthUserRepo()
	rtRepo := newAuthRefreshRepo()
	svc := newTestAuthSvc(uRepo, authWalletRepo{}, rtRepo)

	if _, err := svc.Register(context.Background(), "Alice", "alice@example.com", "password123"); err != nil {
		t.Fatalf("register: %v", err)
	}
	pair, err := svc.Login(context.Background(), "alice@example.com", "password123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	newPair, err := svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if newPair.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token should rotate")
	}
	if newPair.AccessToken == "" {
		t.Fatal("empty access token after refresh")
	}
}

func TestAuthService_Refresh_EmptyToken(t *testing.T) {
	svc := newTestAuthSvc(newAuthUserRepo(), authWalletRepo{}, newAuthRefreshRepo())

	_, err := svc.Refresh(context.Background(), "")
	if !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestAuthService_Refresh_UnknownToken(t *testing.T) {
	svc := newTestAuthSvc(newAuthUserRepo(), authWalletRepo{}, newAuthRefreshRepo())

	_, err := svc.Refresh(context.Background(), "nonexistenttoken")
	if !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestAuthService_Refresh_ExpiredToken(t *testing.T) {
	uRepo := newAuthUserRepo()
	rtRepo := newAuthRefreshRepo()
	svc := newTestAuthSvc(uRepo, authWalletRepo{}, rtRepo)

	if _, err := svc.Register(context.Background(), "Alice", "alice@example.com", "password123"); err != nil {
		t.Fatalf("register: %v", err)
	}
	pair, err := svc.Login(context.Background(), "alice@example.com", "password123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Manually expire the stored token.
	tokenHash := hashRefreshToken(pair.RefreshToken)
	past := time.Now().Add(-time.Hour)
	rtRepo.store[tokenHash].ExpiresAt = past

	_, err = svc.Refresh(context.Background(), pair.RefreshToken)
	if !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized for expired token, got %v", err)
	}
}

// ── Logout ────────────────────────────────────────────────────────────────────

func TestAuthService_Logout_HappyPath(t *testing.T) {
	uRepo := newAuthUserRepo()
	rtRepo := newAuthRefreshRepo()
	svc := newTestAuthSvc(uRepo, authWalletRepo{}, rtRepo)

	if _, err := svc.Register(context.Background(), "Alice", "alice@example.com", "password123"); err != nil {
		t.Fatalf("register: %v", err)
	}
	pair, err := svc.Login(context.Background(), "alice@example.com", "password123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := svc.Logout(context.Background(), pair.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}

	// Token should now be revoked — refresh must fail.
	_, err = svc.Refresh(context.Background(), pair.RefreshToken)
	if !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized after logout, got %v", err)
	}
}

func TestAuthService_Logout_EmptyToken(t *testing.T) {
	svc := newTestAuthSvc(newAuthUserRepo(), authWalletRepo{}, newAuthRefreshRepo())

	if err := svc.Logout(context.Background(), ""); err != nil {
		t.Fatalf("logout with empty token should be no-op, got %v", err)
	}
}

func TestAuthService_Logout_UnknownToken(t *testing.T) {
	svc := newTestAuthSvc(newAuthUserRepo(), authWalletRepo{}, newAuthRefreshRepo())

	// Should be idempotent — unknown token is a no-op.
	if err := svc.Logout(context.Background(), "unknowntoken"); err != nil {
		t.Fatalf("logout with unknown token should be no-op, got %v", err)
	}
}

func TestAuthService_Logout_AlreadyRevoked(t *testing.T) {
	uRepo := newAuthUserRepo()
	rtRepo := newAuthRefreshRepo()
	svc := newTestAuthSvc(uRepo, authWalletRepo{}, rtRepo)

	if _, err := svc.Register(context.Background(), "Alice", "alice@example.com", "password123"); err != nil {
		t.Fatalf("register: %v", err)
	}
	pair, _ := svc.Login(context.Background(), "alice@example.com", "password123")

	_ = svc.Logout(context.Background(), pair.RefreshToken)

	// Second logout must not error.
	if err := svc.Logout(context.Background(), pair.RefreshToken); err != nil {
		t.Fatalf("second logout should be no-op, got %v", err)
	}
}
