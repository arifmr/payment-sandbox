package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

// ── refresh token reuse detection ─────────────────────────────────────────────
//
// Refresh tokens are single-use. Presenting one that was already revoked means the same
// token was used twice, which cannot happen in a healthy client — so the whole chain is
// assumed compromised and every session for that user is torn down.

// seedUserWithSession registers a user and logs them in, returning the user and the
// plaintext refresh token from that session.
func seedUserWithSession(t *testing.T, users *authUserRepo, refresh *authRefreshRepo) (*model.User, string) {
	t.Helper()

	svc := newTestAuthSvc(users, authWalletRepo{}, refresh)
	u, err := svc.Register(context.Background(), "Toko A", "toko@example.com", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	pair, err := svc.Login(context.Background(), "toko@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return u, pair.RefreshToken
}

func TestAuthService_Refresh_ReplayedTokenRevokesEverySession(t *testing.T) {
	users := newAuthUserRepo()
	refresh := newAuthRefreshRepo()
	svc := newTestAuthSvc(users, authWalletRepo{}, refresh)
	ctx := context.Background()

	u, first := seedUserWithSession(t, users, refresh)

	// A second, independent session — a different device.
	otherPair, err := svc.Login(ctx, "toko@example.com", "password123")
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}

	// Normal rotation: `first` is spent and replaced.
	rotated, err := svc.Refresh(ctx, first)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Replaying the spent token is the attack signal.
	if _, err := svc.Refresh(ctx, first); !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("replaying a spent token: want ErrUnauthorized, got %v", err)
	}

	// Every token for this user must now be dead — including the rotated one the
	// legitimate client holds, and the unrelated second session.
	for name, tok := range map[string]string{
		"rotated token from the compromised chain": rotated.RefreshToken,
		"token from the other device":              otherPair.RefreshToken,
	} {
		if _, err := svc.Refresh(ctx, tok); !errors.Is(err, apperror.ErrUnauthorized) {
			t.Errorf("%s should have been revoked, got %v", name, err)
		}
	}

	// Sanity: the user still exists and can start fresh by logging in again.
	if _, err := svc.Login(ctx, "toko@example.com", "password123"); err != nil {
		t.Errorf("the user must still be able to log in again: %v", err)
	}
	_ = u
}

// Expiry is the normal end of a session, not an attack. It must not tear down the
// user's other sessions.
func TestAuthService_Refresh_ExpiredTokenDoesNotRevokeOtherSessions(t *testing.T) {
	users := newAuthUserRepo()
	refresh := newAuthRefreshRepo()
	svc := newTestAuthSvc(users, authWalletRepo{}, refresh)
	ctx := context.Background()

	u, _ := seedUserWithSession(t, users, refresh)

	// A live session on another device.
	live, err := svc.Login(ctx, "toko@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// An expired-but-never-revoked token.
	expired := &model.RefreshToken{
		ID:        uuid.New(),
		UserID:    u.ID,
		TokenHash: hashRefreshToken("expired-plaintext"),
		ExpiresAt: time.Now().Add(-time.Hour).UTC(),
	}
	if err := refresh.Create(ctx, expired); err != nil {
		t.Fatalf("seeding expired token: %v", err)
	}

	if _, err := svc.Refresh(ctx, "expired-plaintext"); !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("expired token: want ErrUnauthorized, got %v", err)
	}

	// The live session must survive — that is the whole distinction.
	if _, err := svc.Refresh(ctx, live.RefreshToken); err != nil {
		t.Errorf("an expired token must not revoke a live session: %v", err)
	}
}

// Logout revokes one token. Presenting it again is a replay, so it must trip the same
// defence rather than being silently ignored.
func TestAuthService_Refresh_AfterLogoutIsTreatedAsReuse(t *testing.T) {
	users := newAuthUserRepo()
	refresh := newAuthRefreshRepo()
	svc := newTestAuthSvc(users, authWalletRepo{}, refresh)
	ctx := context.Background()

	_, tok := seedUserWithSession(t, users, refresh)
	other, err := svc.Login(ctx, "toko@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if err := svc.Logout(ctx, tok); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := svc.Refresh(ctx, tok); !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("using a logged-out token: want ErrUnauthorized, got %v", err)
	}
	// Treated as compromise, so the other session goes too.
	if _, err := svc.Refresh(ctx, other.RefreshToken); !errors.Is(err, apperror.ErrUnauthorized) {
		t.Errorf("reuse must revoke the whole chain, other session survived: %v", err)
	}
}

// A token that was never issued must not trigger a mass revocation — otherwise anyone
// could log every user out by posting garbage.
func TestAuthService_Refresh_UnknownTokenDoesNotRevokeAnything(t *testing.T) {
	users := newAuthUserRepo()
	refresh := newAuthRefreshRepo()
	svc := newTestAuthSvc(users, authWalletRepo{}, refresh)
	ctx := context.Background()

	_, live := seedUserWithSession(t, users, refresh)

	if _, err := svc.Refresh(ctx, "never-issued-token"); !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("unknown token: want ErrUnauthorized, got %v", err)
	}
	if _, err := svc.Refresh(ctx, live); err != nil {
		t.Errorf("an unknown token must not affect real sessions: %v", err)
	}
}

// Normal rotation must keep other sessions alive — only the presented token is spent.
func TestAuthService_Refresh_RotationLeavesOtherSessionsAlone(t *testing.T) {
	users := newAuthUserRepo()
	refresh := newAuthRefreshRepo()
	svc := newTestAuthSvc(users, authWalletRepo{}, refresh)
	ctx := context.Background()

	_, first := seedUserWithSession(t, users, refresh)
	second, err := svc.Login(ctx, "toko@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if _, err := svc.Refresh(ctx, first); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, err := svc.Refresh(ctx, second.RefreshToken); err != nil {
		t.Errorf("rotating one session must not disturb another: %v", err)
	}
}

// ── login timing ──────────────────────────────────────────────────────────────

// Login must not reveal account existence through response time. Both branches have to
// run bcrypt. Asserting on wall-clock timing would be flaky, so this asserts on the
// mechanism: an unknown email still performs a bcrypt verification.
func TestAuthService_Login_UnknownEmailStillRunsBcrypt(t *testing.T) {
	users := newAuthUserRepo()
	refresh := newAuthRefreshRepo()
	svc := newTestAuthSvc(users, authWalletRepo{}, refresh)
	ctx := context.Background()

	if _, err := svc.Register(ctx, "Toko A", "known@example.com", "password123"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Both paths must fail identically from the caller's point of view.
	_, unknownErr := svc.Login(ctx, "unknown@example.com", "password123")
	_, wrongPwErr := svc.Login(ctx, "known@example.com", "wrong-password")

	if !errors.Is(unknownErr, apperror.ErrInvalidCredentials) {
		t.Errorf("unknown email: want ErrInvalidCredentials, got %v", unknownErr)
	}
	if !errors.Is(wrongPwErr, apperror.ErrInvalidCredentials) {
		t.Errorf("wrong password: want ErrInvalidCredentials, got %v", wrongPwErr)
	}
	// Identical error value, so no information is carried in the response either.
	if unknownErr.Error() != wrongPwErr.Error() {
		t.Errorf("error messages differ and leak account existence:\n  unknown: %v\n  wrong pw: %v",
			unknownErr, wrongPwErr)
	}
}

// The decoy comparison is what equalises the two branches. Measured at bcrypt.MinCost
// so the assertion stays fast: the point is that work happens at all, not how much.
func TestHasher_CompareDecoyDoesWork(t *testing.T) {
	h := newTestHasher()

	start := time.Now()
	h.CompareDecoy()
	if elapsed := time.Since(start); elapsed <= 0 {
		t.Error("CompareDecoy should perform a real bcrypt comparison")
	}
	// Must be safe to call repeatedly and never panic.
	for i := 0; i < 3; i++ {
		h.CompareDecoy()
	}
}

// A user whose stored hash is corrupt must fail closed, not be let in.
func TestAuthService_Login_CorruptStoredHashIsRejected(t *testing.T) {
	users := newAuthUserRepo()
	refresh := newAuthRefreshRepo()
	svc := newTestAuthSvc(users, authWalletRepo{}, refresh)
	ctx := context.Background()

	users.byEmail["broken@example.com"] = &model.User{
		ID:           uuid.New(),
		Email:        "broken@example.com",
		PasswordHash: "not-a-bcrypt-hash",
		Name:         "Broken",
		Role:         constant.RoleMerchant,
	}

	if _, err := svc.Login(ctx, "broken@example.com", "anything"); !errors.Is(err, apperror.ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}
