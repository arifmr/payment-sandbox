package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/jwt"
)

func init() { gin.SetMode(gin.TestMode) }

const authTestSecret = "middleware-test-secret-32-chars-long"

// probe records what the protected handler observed, so tests can assert that
// identity was propagated rather than just checking the status code.
type probe struct {
	reached bool
	userID  uuid.UUID
	hasUser bool
	role    string
}

// runAuth wires Auth (and optionally RequireRole) in front of a probe handler and
// performs one request carrying the given Authorization header.
func runAuth(t *testing.T, header string, optional bool, roles ...string) (*httptest.ResponseRecorder, *probe) {
	t.Helper()

	j := jwt.New(authTestSecret, time.Hour)
	seen := &probe{}

	r := gin.New()
	chain := []gin.HandlerFunc{Auth(j, optional)}
	if len(roles) > 0 {
		chain = append(chain, RequireRole(roles...))
	}
	chain = append(chain, func(c *gin.Context) {
		seen.reached = true
		seen.userID, seen.hasUser = UserID(c)
		seen.role = Role(c)
		c.Status(http.StatusOK)
	})
	r.GET("/protected", chain...)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec, seen
}

// bearerFor mints a valid token for the given user and role.
func bearerFor(t *testing.T, userID uuid.UUID, role constant.Role) string {
	t.Helper()
	tok, _, err := jwt.New(authTestSecret, time.Hour).Issue(userID.String(), string(role))
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}
	return "Bearer " + tok
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) model.ErrorResponse {
	t.Helper()
	var body model.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body %q: %v", rec.Body.String(), err)
	}
	return body
}

// ── Auth (required) ───────────────────────────────────────────────────────────

func TestAuth_ValidTokenPropagatesIdentity(t *testing.T) {
	uid := uuid.New()
	rec, seen := runAuth(t, bearerFor(t, uid, constant.RoleMerchant), false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !seen.reached {
		t.Fatal("handler was not reached")
	}
	if !seen.hasUser || seen.userID != uid {
		t.Errorf("UserID = %v (ok=%v), want %v", seen.userID, seen.hasUser, uid)
	}
	if seen.role != string(constant.RoleMerchant) {
		t.Errorf("Role = %q, want MERCHANT", seen.role)
	}
}

func TestAuth_RejectsBadHeaders(t *testing.T) {
	uid := uuid.New()
	valid := bearerFor(t, uid, constant.RoleMerchant)
	rawToken := valid[len("Bearer "):]

	cases := []struct{ name, header string }{
		{"missing header", ""},
		{"token without scheme", rawToken},
		{"wrong scheme", "Basic " + rawToken},
		{"scheme only", "Bearer"},
		{"empty token", "Bearer "},
		{"garbage token", "Bearer not-a-jwt"},
		{"token signed elsewhere", "Bearer " + mustIssueWithSecret(t, "another-secret-that-is-32-chars!!")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, seen := runAuth(t, tc.header, false)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if seen.reached {
				t.Fatal("handler must not run for an unauthenticated request")
			}
			if code := decodeError(t, rec).Error.Code; code != "UNAUTHORIZED" {
				t.Errorf("error code = %q, want UNAUTHORIZED", code)
			}
		})
	}
}

// The scheme is case-insensitive per RFC 7235.
func TestAuth_AcceptsLowercaseBearerScheme(t *testing.T) {
	uid := uuid.New()
	header := "bearer " + bearerFor(t, uid, constant.RoleMerchant)[len("Bearer "):]

	rec, seen := runAuth(t, header, false)
	if rec.Code != http.StatusOK || !seen.reached {
		t.Fatalf("status = %d, reached = %v; want 200 and reached", rec.Code, seen.reached)
	}
}

func TestAuth_RejectsExpiredToken(t *testing.T) {
	expired, _, err := jwt.New(authTestSecret, -time.Minute).Issue(uuid.NewString(), "MERCHANT")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	rec, seen := runAuth(t, "Bearer "+expired, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an expired token", rec.Code)
	}
	if seen.reached {
		t.Fatal("handler must not run with an expired token")
	}
}

// ── Auth (optional) ───────────────────────────────────────────────────────────
// The public payment endpoints use optional auth: anonymous payers are allowed,
// but a token that IS supplied must still be valid.

func TestAuth_Optional_AllowsAnonymous(t *testing.T) {
	rec, seen := runAuth(t, "", true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an anonymous request", rec.Code)
	}
	if !seen.reached {
		t.Fatal("handler must run without a token when auth is optional")
	}
	if seen.hasUser {
		t.Error("no user identity should be set for an anonymous request")
	}
}

func TestAuth_Optional_ParsesSuppliedToken(t *testing.T) {
	uid := uuid.New()
	rec, seen := runAuth(t, bearerFor(t, uid, constant.RoleMerchant), true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !seen.hasUser || seen.userID != uid {
		t.Errorf("payer identity not attached: UserID = %v (ok=%v)", seen.userID, seen.hasUser)
	}
}

func TestAuth_Optional_StillRejectsInvalidToken(t *testing.T) {
	rec, seen := runAuth(t, "Bearer tampered.token.value", true)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — an invalid token is an error even when auth is optional", rec.Code)
	}
	if seen.reached {
		t.Fatal("handler must not run with an invalid token")
	}
}

// ── RequireRole ───────────────────────────────────────────────────────────────

func TestRequireRole_AllowsMatchingRole(t *testing.T) {
	rec, seen := runAuth(t, bearerFor(t, uuid.New(), constant.RoleAdmin), false, string(constant.RoleAdmin))

	if rec.Code != http.StatusOK || !seen.reached {
		t.Fatalf("status = %d, reached = %v; want 200 and reached", rec.Code, seen.reached)
	}
}

// SRS §2.1/§3.3: a merchant token must not reach admin-only routes.
func TestRequireRole_BlocksMismatchedRole(t *testing.T) {
	rec, seen := runAuth(t, bearerFor(t, uuid.New(), constant.RoleMerchant), false, string(constant.RoleAdmin))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	if seen.reached {
		t.Fatal("a MERCHANT token must not reach an ADMIN-only handler")
	}
	if code := decodeError(t, rec).Error.Code; code != "FORBIDDEN" {
		t.Errorf("error code = %q, want FORBIDDEN", code)
	}
}

func TestRequireRole_AcceptsAnyOfSeveralRoles(t *testing.T) {
	rec, _ := runAuth(t, bearerFor(t, uuid.New(), constant.RoleMerchant), false,
		string(constant.RoleAdmin), string(constant.RoleMerchant))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when the role is in the allow-list", rec.Code)
	}
}

// Without an authenticated role in context, RequireRole must deny rather than
// treat the empty role as a match.
func TestRequireRole_DeniesWhenNoRoleInContext(t *testing.T) {
	r := gin.New()
	r.GET("/admin", RequireRole(string(constant.RoleAdmin)), func(c *gin.Context) {
		t.Error("handler must not be reached without a role")
		c.Status(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireRole_EmptyAllowListDeniesEveryone(t *testing.T) {
	rec, seen := runAuth(t, bearerFor(t, uuid.New(), constant.RoleAdmin), false)
	// No roles passed → RequireRole is not installed by runAuth, so sanity-check
	// the explicit empty case separately.
	if rec.Code != http.StatusOK || !seen.reached {
		t.Fatalf("precondition failed: %d", rec.Code)
	}

	r := gin.New()
	r.GET("/x", RequireRole(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("RequireRole() with no roles: status = %d, want 403", rec2.Code)
	}
}

// ── UserID / Role helpers ─────────────────────────────────────────────────────

func TestUserID_RejectsMalformedContextValue(t *testing.T) {
	cases := []struct {
		name  string
		value any
		set   bool
	}{
		{"absent", nil, false},
		{"not a string", 42, true},
		{"not a uuid", "definitely-not-a-uuid", true},
		{"empty string", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.set {
				c.Set(ctxUserID, tc.value)
			}
			if id, ok := UserID(c); ok {
				t.Errorf("UserID returned (%v, true), want ok=false", id)
			}
		})
	}
}

func TestRole_EmptyWhenAbsentOrWrongType(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := Role(c); got != "" {
		t.Errorf("Role() = %q on a fresh context, want empty", got)
	}
	c.Set(ctxRole, 123)
	if got := Role(c); got != "" {
		t.Errorf("Role() = %q for a non-string value, want empty", got)
	}
}

func mustIssueWithSecret(t *testing.T, secret string) string {
	t.Helper()
	tok, _, err := jwt.New(secret, time.Hour).Issue(uuid.NewString(), "ADMIN")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return tok
}
