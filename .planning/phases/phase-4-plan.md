# Phase 4 — Auth: Full Working Login → 2FA → Cookie Flow

**Status**: PENDING
**Goal**: Implement full authentication flow so that login → 2FA dispatch → verify → session cookie → /me all work end-to-end.

---

## Current State (before this phase)

Phases 1-3 are committed at `f9e54ad` / commit `315f6e0`. What exists:

- `cmd/server/main.go` — HTTP server, graceful shutdown, DB pool
- `internal/config/config.go` — all env vars including `ResendAPIKey`, `ResendFromEmail`, `TwilioAccountSID`, `TwilioAuthToken`, `TwilioFromPhone`
- `internal/domain/entities.go` + `enums.go` — all domain types
- `internal/platform/jwt.go` — `JWTService{Sign(user) string, Verify(token) *Claims}`, HS256, 24h expiry. Claims has `{UserID, Role, CNP}`.
- `internal/middleware/auth.go` — `RequireAuth(jwtSvc)`, `RequireRole(roles...)`, `UserFromContext(ctx)`. Cookie name: `ra_session`. User in context is `*domain.User{ID, Role, CNP}` (partial — only what's in the JWT).
- `internal/api/router.go` — Huma+Chi router. `Handlers{cfg, pool, jwt}`. All routes registered as 501 stubs.
- `internal/api/auth.go` — stub handlers: `login`, `switch2FAMethod`, `verify2FA`, `logout`, `getMe`. Input/output types already defined.
- `internal/db/sqlc/` — generated code in package `dbsqlc`. Key types and funcs:
  - `dbsqlc.New(db DBTX) *Queries` — `DBTX` is `interface{QueryRowContext, QueryContext, ExecContext}` — both `*pgxpool.Pool` and `pgx.Tx` implement it.
  - `Queries.GetUserByCNP(ctx, cnp string) (User, error)`
  - `Queries.GetUserByID(ctx, id uuid.UUID) (User, error)`
  - `Queries.CreateChallenge(ctx, CreateChallengeParams) (AuthChallenge, error)` — params: `{ID uuid.UUID, UserID uuid.UUID, Method AuthMethod, CodeHash string, ExpiresAt time.Time}`
  - `Queries.GetChallenge(ctx, id uuid.UUID) (AuthChallenge, error)`
  - `Queries.MarkChallengeVerified(ctx, id uuid.UUID) error`
  - `dbsqlc.AuthMethod` constants: `AuthMethodPush`, `AuthMethodSms`, `AuthMethodEmail`
  - `dbsqlc.UserRole` constants: `UserRoleApicultor`, `UserRoleFermier`, `UserRoleInspector`
  - `dbsqlc.User` struct: `{ID uuid.UUID, Cnp string, FullName string, Email string, Phone string, Role UserRole, County string, Locality string, PasswordHash string, CreatedAt time.Time}`
  - `dbsqlc.AuthChallenge` struct: `{ID uuid.UUID, UserID uuid.UUID, Method AuthMethod, CodeHash string, ExpiresAt time.Time, VerifiedAt sql.NullTime, CreatedAt time.Time}`
- Module: `github.com/radarul-albinelor/api`
- DB port: **5433** (not 5432)
- Dependencies already in go.mod: `golang.org/x/crypto` (bcrypt), `github.com/wneessen/go-mail`, `github.com/google/uuid`

---

## Prerequisites

Phase 3 complete. DB migrations applied (`make migrate-up`). DB running on port 5433.

---

## Files to Create

### `internal/external/email/client.go`

Package: `email`

```go
package email

import (
    "context"
    "crypto/tls"

    mail "github.com/wneessen/go-mail"
)

type EmailClient struct {
    host     string
    port     int
    user     string
    password string
    from     string
}

func NewClient(host string, port int, user, password, from string) *EmailClient

// Send sends a plain-text email. Returns error on failure.
func (c *EmailClient) Send(ctx context.Context, to, subject, body string) error
```

Implementation notes:
- Use `mail.NewClient(c.host, mail.WithPort(c.port), mail.WithSMTPAuth(mail.SMTPAuthPlain), mail.WithUsername(c.user), mail.WithPassword(c.password), mail.WithTLSConfig(&tls.Config{InsecureSkipVerify: false}), mail.WithSSL())` — `WithSSL()` enables implicit TLS on port 465.
- Create message: `m := mail.NewMsg(); m.From(c.from); m.To(to); m.Subject(subject); m.SetBodyString(mail.TypeTextPlain, body)`
- Send with `client.DialAndSendWithContext(ctx, m)`

### `internal/services/auth.go`

Package: `services`

Struct:
```go
type AuthService struct {
    db    *dbsqlc.Queries
    pool  *pgxpool.Pool
    jwt   *platform.JWTService
    email *email.EmailClient
    cfg   *config.Config
}

func NewAuthService(pool *pgxpool.Pool, jwt *platform.JWTService, email *email.EmailClient, cfg *config.Config) *AuthService
```

Return types:
```go
type LoginResult struct {
    ChallengeID       string `json:"challenge_id"`
    Method            string `json:"method"`
    MaskedDestination string `json:"masked_destination"`
}
```

Methods to implement:

**`Login(ctx context.Context, cnp, password string) (*LoginResult, error)`**
1. `q := dbsqlc.New(h.pool)` — use pool directly (read-only, non-transactional)
2. `user, err := q.GetUserByCNP(ctx, cnp)` — if `errors.Is(err, pgx.ErrNoRows)` → return `huma.NewError(401, "invalid_credentials")`
3. `bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))` — if err → return 401 "invalid_credentials"
4. `code := generateCode()` — 6-digit string
5. `hash, err := bcrypt.GenerateFromPassword([]byte(code), 12)` — cost 12
6. Determine method from user: push is default if push subscription exists (for now: always use `sms` or `email` — check phone non-empty → use sms, else email)
7. `challengeID := uuid.New()`
8. `q.CreateChallenge(ctx, dbsqlc.CreateChallengeParams{ID: challengeID, UserID: user.ID, Method: dbsqlc.AuthMethodSms, CodeHash: string(hash), ExpiresAt: time.Now().Add(10 * time.Minute)})`
9. Dispatch code: call `dispatchCode(ctx, user, string(method), code)`
10. Return `&LoginResult{ChallengeID: challengeID.String(), Method: string(method), MaskedDestination: maskedPhone(user.Phone)}`

**`dispatchCode(ctx, user dbsqlc.User, method, code string)`** (private helper)
- `"push"` → `slog.Info("[2FA PUSH]", "code", code, "user", user.FullName)` (no real push in phase 4)
- `"sms"` → HTTP POST to `https://api.twilio.com/2010-04-01/Accounts/{SID}/Messages.json` with basic auth `(AccountSID, AuthToken)`. Form body: `From=cfg.TwilioFromPhone`, `To=user.Phone`, `Body=fmt.Sprintf("Codul dumneavoastră Radarul Albinelor: %s. Expiră în 10 minute.", code)`. If Twilio creds empty → log code to stdout: `slog.Info("[2FA SMS mock]", "to", user.Phone, "code", code)`.
- `"email"` → `emailClient.Send(ctx, user.Email, "Cod autentificare Radarul Albinelor", fmt.Sprintf("Codul dumneavoastră: %s\n\nExpiră în 10 minute.", code))`. If email client is nil or send fails → log to stdout.

**`Switch2FAMethod(ctx context.Context, challengeIDStr, method string) (*LoginResult, error)`**
1. Parse challengeID as uuid.UUID
2. `q.GetChallenge(ctx, challengeID)` — if not found → 404
3. Check not expired: `challenge.ExpiresAt.Before(time.Now())` → 400 "challenge_expired"
4. Check not verified: `challenge.VerifiedAt.Valid` → 400 "already_verified"
5. Get user: `q.GetUserByID(ctx, challenge.UserID)`
6. Generate new code + hash, create new challenge with new method, dispatch, return LoginResult

**`Verify2FA(ctx context.Context, challengeIDStr, code string) (*domain.User, string, error)`**
1. Parse UUID
2. `GetChallenge` — not found → 401 "invalid_2fa_code"
3. Expired → 401 "invalid_2fa_code" (don't leak which condition)
4. Already verified → 401 "invalid_2fa_code"
5. `bcrypt.CompareHashAndPassword([]byte(challenge.CodeHash), []byte(code))` — if err → 401 "invalid_2fa_code"
6. `q.MarkChallengeVerified(ctx, challengeID)`
7. `dbUser, _ := q.GetUserByID(ctx, challenge.UserID)`
8. Build `domain.User` from `dbsqlc.User` (convert all fields — `ID = dbUser.ID.String()`, `CNP = dbUser.Cnp`, `Name = dbUser.FullName`, `Role = domain.Role(dbUser.Role)`, etc.)
9. `token, _ := jwt.Sign(domainUser)`
10. Return `domainUser, token, nil`

**`GetUser(ctx context.Context, userIDStr string) (*domain.User, error)`**
- Parse UUID, GetUserByID, convert to domain.User

**Helper functions:**

```go
// generateCode returns 6 cryptographically random decimal digits as string
func generateCode() string {
    b := make([]byte, 4)
    rand.Read(b) // crypto/rand
    n := binary.BigEndian.Uint32(b)
    return fmt.Sprintf("%06d", n % 1_000_000)
}

// maskedPhone: "+40 7•• ••• •42" — show country prefix and last 2 digits
func maskedPhone(phone string) string {
    // phone is like "+40721234567"
    // show first 4 chars (+40 ) then bullets, then last 2
    if len(phone) < 4 { return "•••" }
    last2 := phone[len(phone)-2:]
    return phone[:4] + "•• ••• •" + last2
}

// maskedEmail: "u***@e***.com"
func maskedEmail(addr string) string {
    parts := strings.SplitN(addr, "@", 2)
    if len(parts) != 2 { return "***@***" }
    local := parts[0]
    domain := parts[1]
    maskedLocal := string(local[0]) + "***"
    domainParts := strings.SplitN(domain, ".", 2)
    if len(domainParts) != 2 { return maskedLocal + "@***" }
    return maskedLocal + "@" + string(domainParts[0][0]) + "***." + domainParts[1]
}
```

**`sendTwilioSMS(ctx, cfg, to, body string) error`** (package-level helper):
```go
func sendTwilioSMS(ctx context.Context, cfg *config.Config, to, body string) error {
    if cfg.TwilioAccountSID == "" {
        slog.Info("[MOCK SMS]", "to", to, "body", body)
        return nil
    }
    endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", cfg.TwilioAccountSID)
    form := url.Values{}
    form.Set("From", cfg.TwilioFromPhone)
    form.Set("To", to)
    form.Set("Body", body)
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.SetBasicAuth(cfg.TwilioAccountSID, cfg.TwilioAuthToken)
    resp, err := http.DefaultClient.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        b, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("twilio error %d: %s", resp.StatusCode, b)
    }
    return nil
}
```

---

## Files to Modify

### `internal/api/auth.go`

Keep existing input/output types but implement all 5 handlers for real.

**`login` handler:**
```go
func (h *Handlers) login(ctx context.Context, input *LoginInput) (*LoginOutput, error) {
    // CNP format already validated by Huma schema (13 digits, pattern)
    result, err := h.authSvc.Login(ctx, input.Body.CNP, input.Body.Password)
    if err != nil { return nil, err } // service returns huma.Error directly
    return &LoginOutput{Body: struct{...}{result.ChallengeID, result.Method, result.MaskedDestination}}, nil
}
```

**`verify2FA` handler:**
- Call `h.authSvc.Verify2FA(ctx, input.Body.ChallengeID, input.Body.Code)`
- On success, set cookie:
  ```go
  cookie := &http.Cookie{
      Name:     "ra_session",
      Value:    token,
      HttpOnly: true,
      SameSite: http.SameSiteLaxMode,
      Path:     "/",
      MaxAge:   86400,
  }
  ```
- To set cookie in Huma v2: the `Verify2FAOutput` already has `SetCookie string \`header:"Set-Cookie"\``. Set it to `cookie.String()`.
- Also need raw http.ResponseWriter to set cookie header — use `huma.Context` approach: in the handler signature, to access raw `http.ResponseWriter`, use the `huma.Context` from context: cast to `humachi.Context` is not directly exposed. **Workaround**: Since `Verify2FAOutput` has `SetCookie string \`header:"Set-Cookie"\``, set: `out.SetCookie = fmt.Sprintf("ra_session=%s; HttpOnly; SameSite=Lax; Path=/; Max-Age=86400", token)`.

**`logout` handler:**
- Needs to clear the cookie. Since LogoutOutput has no header field, add one:
  ```go
  type LogoutOutput struct {
      SetCookie string `header:"Set-Cookie"`
  }
  ```
  Set: `out.SetCookie = "ra_session=; HttpOnly; SameSite=Lax; Path=/; Max-Age=-1"`

**`getMe` handler:**
- `user := middleware.UserFromContext(ctx)` — NOTE: in Huma v2, the `ctx context.Context` passed to the handler is the request context. `middleware.UserFromContext(ctx)` works directly.
- But `user` from context only has `{ID, Role, CNP}` (from JWT). Call `h.authSvc.GetUser(ctx, user.ID)` to get full user.
- Return `&MeOutput{Body: struct{User any}{domainUser}}`

**`switch2FAMethod` handler:**
- Call `h.authSvc.Switch2FAMethod(ctx, input.Body.ChallengeID, input.Body.Method)`

### `internal/api/router.go`

Add `authSvc *services.AuthService` field to `Handlers` struct.

In `NewRouter`:
1. `emailClient := email.NewClient("smtp.resend.com", 465, "apikey", cfg.ResendAPIKey, cfg.ResendFromEmail)`
2. `authSvc := services.NewAuthService(pool, jwtSvc, emailClient, cfg)`
3. `h := &Handlers{cfg: cfg, pool: pool, jwt: jwtSvc, authSvc: authSvc}`

Add imports for `internal/external/email` and `internal/services`.

---

## Key Implementation Details

1. **`dbsqlc.New(pool)` works directly**: `*pgxpool.Pool` implements `dbsqlc.DBTX` interface (`QueryRowContext`, `QueryContext`, `ExecContext`). No need for a dedicated `sql.DB`.

2. **domain.User vs dbsqlc.User**: The middleware auth.go stores `domain.User` in context. The conversion from `dbsqlc.User` to `domain.User`:
   ```go
   func dbUserToDomain(u dbsqlc.User) *domain.User {
       return &domain.User{
           ID:           u.ID.String(),
           CNP:          u.Cnp,
           Name:         u.FullName,
           FullName:     u.FullName,
           Email:        u.Email,
           Phone:        u.Phone,
           Role:         domain.Role(u.Role),
           County:       u.County,
           Locality:     u.Locality,
           PasswordHash: u.PasswordHash,
       }
   }
   ```
   Put this helper in `internal/services/auth.go` or a shared `internal/services/convert.go`.

3. **JWT Sign requires `domain.User`**: `JWTService.Sign(user *domain.User) (string, error)`. The claims embed `{UserID: user.ID, Role: user.Role, CNP: user.CNP}`.

4. **Error codes**: Return `huma.NewError(statusCode, message)` from service layer. The `message` field in `huma.NewError` becomes the `title` in the problem JSON. For custom codes, wrap with:
   ```go
   return nil, huma.NewError(http.StatusUnauthorized, "invalid_credentials")
   ```
   The handler just propagates this error — Huma renders it as `{"title":"invalid_credentials","status":401}`.

5. **2FA code security**: Use `crypto/rand`, not `math/rand`. Never log the plaintext code unless Twilio creds are empty (dev mode).

6. **bcrypt cost**: Use cost 12 for challenge code hash. Users' password_hash (from seed) also uses cost 12.

7. **pgx.ErrNoRows detection**: `dbsqlc` uses `database/sql` driver interface but with `pgxpool`. The "no rows" error is `pgx.ErrNoRows` from `github.com/jackc/pgx/v5`. Check: `errors.Is(err, pgx.ErrNoRows)`. Alternatively check: `err.Error() == "no rows in result set"` as fallback. Import `github.com/jackc/pgx/v5` for the sentinel.

8. **Cookie in Huma v2**: Huma processes struct fields with `header:"Set-Cookie"` and writes them. The value must be the full cookie string including all directives. Use `net/http.Cookie.String()` method if you prefer:
   ```go
   c := &http.Cookie{Name: "ra_session", Value: token, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: 86400}
   out.SetCookie = c.String()
   ```

9. **Don't log CNP**: Never put `cnp` in slog fields. Log `user_id` instead.

---

## Test Setup — Manually Insert a User Before Testing

Before Phase 5 seeds users automatically, insert one manually to test Phase 4:

```sql
-- Run via: docker exec -it radarul-postgres psql -U radarul radarul
-- Or: psql "postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable"
INSERT INTO users (cnp, full_name, email, phone, role, county, locality, password_hash)
VALUES (
  '1850101123456',
  'Andrei Berar',
  'andrei.berar@test.com',
  '+40721000001',
  'apicultor',
  'Cluj',
  'Apahida',
  '$2a$12$eJm0z5oUm3tPmBPjQu5xJ.7LRvDjVDmpvT5G.YKhPd5GVKbpNH/FW'
  -- bcrypt of "parola123" at cost 12
);
```

To generate the hash yourself: `go run -` then paste:
```go
package main
import ("fmt"; "golang.org/x/crypto/bcrypt")
func main() { h, _ := bcrypt.GenerateFromPassword([]byte("parola123"), 12); fmt.Println(string(h)) }
```

Or run: `make seed` after implementing Phase 5.

---

## Verification Steps

```bash
# Start server
go run ./cmd/server

# 1. Login — should return challenge_id
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"1850101123456","password":"parola123"}' | jq .
# Expected: {"challenge_id":"...", "method":"sms", "masked_destination":"+40 7•• ••• •01"}
# Also check server stdout for "[2FA SMS mock]" line with 6-digit code

# 2. Verify 2FA (use code from stdout)
curl -s -X POST http://localhost:8080/api/v1/auth/2fa/verify \
  -H 'Content-Type: application/json' \
  -c /tmp/cookies.txt \
  -d '{"challenge_id":"<UUID from step 1>","code":"<6-digit code from stdout>"}' | jq .
# Expected: {"user":{"id":"...","role":"apicultor",...}} + Set-Cookie: ra_session=...

# 3. Get current user
curl -s http://localhost:8080/api/v1/auth/me -b /tmp/cookies.txt | jq .
# Expected: {"user":{"id":"...","full_name":"Andrei Berar","role":"apicultor",...}}

# 4. Switch 2FA method (test — get new challenge first from step 1)
curl -s -X POST http://localhost:8080/api/v1/auth/2fa/method \
  -H 'Content-Type: application/json' \
  -d '{"challenge_id":"<UUID>","method":"email"}' | jq .
# Expected: new challenge_id, method="email", masked_destination="a***@t***.com"

# 5. Logout
curl -s -X POST http://localhost:8080/api/v1/auth/logout \
  -b /tmp/cookies.txt -c /tmp/cookies.txt | jq .

# 6. /me after logout → 401
curl -s http://localhost:8080/api/v1/auth/me -b /tmp/cookies.txt | jq .
# Expected: {"status":401,"title":"Sesiune invalidă sau expirată"}

# 7. Wrong password → 401 invalid_credentials
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"1850101123456","password":"wrong"}' | jq .
# Expected: {"status":401,"title":"invalid_credentials"}

# 8. Wrong CNP format → 422 (Huma schema validation)
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"cnp":"123","password":"parola123"}' | jq .
# Expected: 422 unprocessable entity

# 9. build check
go build ./...
```

---

## After Completion

Write `/Users/alinscreciu/work/hackathon/api/.planning/phases/phase-4.md`:

```markdown
# Phase 4 — Auth
Status: COMPLETE
Completed: <date>

## What was built
- internal/external/email/client.go — EmailClient using go-mail, SMTP implicit TLS port 465
- internal/services/auth.go — AuthService: Login, Switch2FAMethod, Verify2FA, GetUser
- internal/api/auth.go — all 5 handlers fully implemented
- internal/api/router.go — AuthService instantiated, injected into Handlers

## Verification
- Login flow works end-to-end: login → code dispatched (stdout mock or real Twilio) → verify → cookie set → /me returns full user
- Logout clears cookie, subsequent /me returns 401
- Wrong credentials → 401 invalid_credentials
- go build ./... clean
```
