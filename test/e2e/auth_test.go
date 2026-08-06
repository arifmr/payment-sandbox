//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// TestE2E_Auth_RefreshRotatesTheTokenPair covers the rotation contract: each refresh
// revokes the token used and issues a fresh pair, so a refresh token is single-use.
func TestE2E_Auth_RefreshRotatesTheTokenPair(t *testing.T) {
	m := newMerchant(t)
	anon := newClient()

	r := anon.post(t, "/api/v1/auth/refresh", map[string]any{"refresh_token": m.RefreshToken})
	requireStatus(t, r, http.StatusOK)
	next := decode[loginResponse](t, r)

	if next.RefreshToken == m.RefreshToken {
		t.Fatal("refresh returned the same refresh token; rotation did not happen")
	}
	if next.AccessToken == "" {
		t.Fatal("refresh returned no access token")
	}

	// The new access token works.
	requireStatus(t, anon.as(next.AccessToken).get(t, "/api/v1/wallet"), http.StatusOK)
}

// TestE2E_Auth_ReuseOfARevokedTokenKillsEverySession is the reuse-detection behaviour from
// agent_documentation/04-security.md section 4, and the reason the frontend must make
// refresh single-flight.
//
// Presenting an already-revoked token means it was used twice. The legitimate owner and a
// thief are indistinguishable at that point — whoever redeemed first got the new pair — so
// refusing just that one token would leave the attacker holding a working session. The
// whole chain is dropped instead.
//
// !!! THIS TEST CURRENTLY FAILS, AND THE FAILURE IS REAL. !!!
//
// authService.Refresh calls RevokeAllForUser inside uow.Do and then returns
// apperror.ErrUnauthorized to reject the caller. sqlUoW.Do rolls back on any error from
// its callback, so the rejection undoes the revocation it just performed. The warning line
// still fires, which means the logs report action=revoked_all_sessions_for_user while
// every session stays alive. Verified against the database: after triggering reuse, the
// user's other refresh tokens still have revoked_at IS NULL.
//
// The unit test for this path passes because it uses noopUoW, which runs the callback and
// returns its error without a transaction — so it can prove RevokeAllForUser was *called*,
// never that the write survived. That gap is documented in
// agent_documentation/05-testing-strategy.md section 3; this is it happening for real.
//
// The assertions below encode the intended behaviour from
// agent_documentation/04-security.md section 4. They are deliberately NOT relaxed to match
// the current output: doing that would lock the defect in as the specification.
func TestE2E_Auth_ReuseOfARevokedTokenKillsEverySession(t *testing.T) {
	m := newMerchant(t)
	anon := newClient()

	// A second, independent session for the same account — this is what must also die.
	other := loginAs(t, m.Email, testPassword)

	// First refresh succeeds and revokes m.RefreshToken.
	r := anon.post(t, "/api/v1/auth/refresh", map[string]any{"refresh_token": m.RefreshToken})
	requireStatus(t, r, http.StatusOK)
	rotated := decode[loginResponse](t, r)

	// Replaying the revoked token is the detection trigger.
	replay := anon.post(t, "/api/v1/auth/refresh", map[string]any{"refresh_token": m.RefreshToken})
	requireStatus(t, replay, http.StatusUnauthorized)

	// The pair minted by the legitimate refresh is collateral damage, by design.
	after := anon.post(t, "/api/v1/auth/refresh", map[string]any{"refresh_token": rotated.RefreshToken})
	if after.Status != http.StatusUnauthorized {
		t.Errorf("the rotated token still works after reuse was detected (status %d); "+
			"the whole chain should have been revoked", after.Status)
	}

	// And so is the unrelated session, which is the part that makes this a real defence.
	unrelated := anon.post(t, "/api/v1/auth/refresh", map[string]any{"refresh_token": other.RefreshToken})
	if unrelated.Status != http.StatusUnauthorized {
		t.Errorf("a separate session survived reuse detection (status %d); "+
			"an attacker holding any token for this user would keep access", unrelated.Status)
	}
}

// TestE2E_Auth_UnknownRefreshTokenRevokesNothing guards the boundary of the behaviour
// above. If an unrecognised token also triggered mass revocation, this endpoint would be a
// denial-of-service tool: anyone could log everybody out by posting garbage.
func TestE2E_Auth_UnknownRefreshTokenRevokesNothing(t *testing.T) {
	m := newMerchant(t)
	anon := newClient()

	garbage := strings.Repeat("f", 64)
	requireStatus(t, anon.post(t, "/api/v1/auth/refresh", map[string]any{"refresh_token": garbage}),
		http.StatusUnauthorized)

	// The real session is untouched.
	r := anon.post(t, "/api/v1/auth/refresh", map[string]any{"refresh_token": m.RefreshToken})
	if r.Status != http.StatusOK {
		t.Fatalf("a valid session was invalidated by someone else posting a bogus token (status %d)", r.Status)
	}
}

// TestE2E_Auth_LogoutRevokesTheRefreshToken checks logout is server-side, not just a
// client clearing storage.
func TestE2E_Auth_LogoutRevokesTheRefreshToken(t *testing.T) {
	m := newMerchant(t)
	anon := newClient()

	// 204: logout has nothing to return, and an empty body is the honest representation.
	requireStatus(t, anon.post(t, "/api/v1/auth/logout", map[string]any{"refresh_token": m.RefreshToken}),
		http.StatusNoContent)

	requireStatus(t, anon.post(t, "/api/v1/auth/refresh", map[string]any{"refresh_token": m.RefreshToken}),
		http.StatusUnauthorized)
}

// TestE2E_Auth_FailedLoginDoesNotRevealWhetherTheAccountExists closes the enumeration
// channel: a wrong password and an unknown address must be indistinguishable, in status,
// code, and message.
func TestE2E_Auth_FailedLoginDoesNotRevealWhetherTheAccountExists(t *testing.T) {
	m := newMerchant(t)
	anon := newClient()

	existing := anon.post(t, "/api/v1/auth/login", map[string]any{
		"email": m.Email, "password": "definitely-not-the-password",
	})
	unknown := anon.post(t, "/api/v1/auth/login", map[string]any{
		"email": uniqueEmail("nobody"), "password": "definitely-not-the-password",
	})

	requireErrorCode(t, existing, http.StatusUnauthorized, "INVALID_CREDENTIALS")
	requireErrorCode(t, unknown, http.StatusUnauthorized, "INVALID_CREDENTIALS")

	existingBody := decode[errorEnvelope](t, existing)
	unknownBody := decode[errorEnvelope](t, unknown)
	if existingBody.Error.Message != unknownBody.Error.Message {
		t.Errorf("messages differ between a known and an unknown account:\n known:   %q\n unknown: %q",
			existingBody.Error.Message, unknownBody.Error.Message)
	}

	// A message that names the problem gives the answer away even with a uniform code.
	for _, phrase := range []string{"not found", "no such", "unknown", "does not exist", "tidak ada"} {
		if strings.Contains(strings.ToLower(unknownBody.Error.Message), phrase) {
			t.Errorf("login failure message leaks account existence via %q: %q", phrase, unknownBody.Error.Message)
		}
	}
}

// TestE2E_Auth_DuplicateRegistrationIsAConflict pins the unique index behind email. The
// application's pre-check has a race; the index is the authority, and its violation has to
// surface as a domain error rather than a 500.
func TestE2E_Auth_DuplicateRegistrationIsAConflict(t *testing.T) {
	m := newMerchant(t)

	resp := newClient().post(t, "/api/v1/auth/register", map[string]any{
		"name": "Impostor", "email": m.Email, "password": testPassword,
	})
	requireErrorCode(t, resp, http.StatusConflict, "EMAIL_TAKEN")
}

// TestE2E_Auth_RegistrationValidatesItsInput checks the binding tags actually run. They
// only execute when the request arrives as JSON over HTTP, so this is the layer that can
// prove it.
func TestE2E_Auth_RegistrationValidatesItsInput(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"malformed email", map[string]any{"name": "A", "email": "not-an-email", "password": testPassword}},
		{"password too short", map[string]any{"name": "A", "email": uniqueEmail("short"), "password": "abc"}},
		{"password over bcrypt's 72-byte limit", map[string]any{
			"name": "A", "email": uniqueEmail("long"), "password": strings.Repeat("x", 73),
		}},
		{"missing name", map[string]any{"email": uniqueEmail("noname"), "password": testPassword}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := newClient().post(t, "/api/v1/auth/register", tc.body)
			if resp.Status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400\nbody: %s", resp.Status, resp.Body)
			}
		})
	}
}

// TestE2E_Auth_ResponsesNeverCarryThePasswordHash asserts against the serialized bytes
// rather than a struct field. What reaches the client is JSON, and a hash can leak through
// a stray tag or an embedded struct that a typed assertion would never notice.
func TestE2E_Auth_ResponsesNeverCarryThePasswordHash(t *testing.T) {
	email := uniqueEmail("leak")
	anon := newClient()

	reg := anon.post(t, "/api/v1/auth/register", map[string]any{
		"name": "Leak Check", "email": email, "password": testPassword,
	})
	requireStatus(t, reg, http.StatusCreated)

	login := anon.post(t, "/api/v1/auth/login", map[string]any{"email": email, "password": testPassword})
	requireStatus(t, login, http.StatusOK)

	for _, r := range []*response{reg, login} {
		body := string(r.Body)
		// A bcrypt hash always starts with one of these.
		for _, marker := range []string{"$2a$", "$2b$", "$2y$", "password_hash", "passwordHash"} {
			if strings.Contains(body, marker) {
				t.Errorf("auth response contains %q: %s", marker, body)
			}
		}
		if strings.Contains(body, testPassword) {
			t.Errorf("auth response echoes the plaintext password: %s", body)
		}
	}
}
