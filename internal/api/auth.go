package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/radarul-albinelor/api/internal/middleware"
)

func registerAuth(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/login",
		Summary:     "Login with CNP and password",
		Tags:        []string{"auth"},
	}, h.login)

	huma.Register(api, huma.Operation{
		OperationID: "switch-2fa-method",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/2fa/method",
		Summary:     "Switch 2FA method",
		Tags:        []string{"auth"},
	}, h.switch2FAMethod)

	huma.Register(api, huma.Operation{
		OperationID: "verify-2fa",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/2fa/verify",
		Summary:     "Verify 2FA code",
		Tags:        []string{"auth"},
	}, h.verify2FA)

	huma.Register(api, huma.Operation{
		OperationID: "logout",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/logout",
		Summary:     "Logout",
		Tags:        []string{"auth"},
	}, h.logout)

	huma.Register(api, huma.Operation{
		OperationID: "get-me",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/me",
		Summary:     "Get current user",
		Tags:        []string{"auth"},
	}, h.getMe)
}

type LoginInput struct {
	Body struct {
		CNP      string `json:"cnp" minLength:"13" maxLength:"13" pattern:"^[0-9]{13}$"`
		Password string `json:"password" minLength:"1"`
	}
}
type LoginOutput struct {
	Body struct {
		ChallengeID       string `json:"challenge_id"`
		Method            string `json:"method"`
		MaskedDestination string `json:"masked_destination"`
	}
}

type Switch2FAInput struct {
	Body struct {
		ChallengeID string `json:"challenge_id"`
		Method      string `json:"method" enum:"push,sms,email"`
	}
}

type Verify2FAInput struct {
	Body struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
	}
}
type Verify2FAOutput struct {
	SetCookie []string `header:"Set-Cookie"`
	Body      struct {
		User any `json:"user"`
	}
}

type LogoutOutput struct {
	SetCookie []string `header:"Set-Cookie"`
}

type MeOutput struct {
	Body struct {
		User any `json:"user"`
	}
}

func (h *Handlers) login(ctx context.Context, input *LoginInput) (*LoginOutput, error) {
	result, err := h.authSvc.Login(ctx, input.Body.CNP, input.Body.Password)
	if err != nil {
		return nil, err
	}
	out := &LoginOutput{}
	out.Body.ChallengeID = result.ChallengeID
	out.Body.Method = result.Method
	out.Body.MaskedDestination = result.MaskedDestination
	return out, nil
}

func (h *Handlers) switch2FAMethod(ctx context.Context, input *Switch2FAInput) (*LoginOutput, error) {
	result, err := h.authSvc.Switch2FAMethod(ctx, input.Body.ChallengeID, input.Body.Method)
	if err != nil {
		return nil, err
	}
	out := &LoginOutput{}
	out.Body.ChallengeID = result.ChallengeID
	out.Body.Method = result.Method
	out.Body.MaskedDestination = result.MaskedDestination
	return out, nil
}

func (h *Handlers) verify2FA(ctx context.Context, input *Verify2FAInput) (*Verify2FAOutput, error) {
	user, token, err := h.authSvc.Verify2FA(ctx, input.Body.ChallengeID, input.Body.Code)
	if err != nil {
		return nil, err
	}
	out := &Verify2FAOutput{}
	// Two Set-Cookie headers: first clears any leftover host-only ra_session
	// (from before COOKIE_DOMAIN was configured), second sets the new
	// domain-scoped session. Per RFC 6265 the host-only cookie is more
	// specific and shadows the domain cookie, so without this clear the BE
	// would keep reading a stale JWT with the old role.
	out.SetCookie = []string{
		clearHostOnlyCookie().String(),
		h.buildSessionCookie(token, 86400).String(),
	}
	out.Body.User = user
	return out, nil
}

func (h *Handlers) logout(_ context.Context, _ *struct{}) (*LogoutOutput, error) {
	out := &LogoutOutput{}
	// Clear both cookie scopes so logout actually logs the user out regardless
	// of which Set-Cookie the browser still has from a past deploy.
	out.SetCookie = []string{
		clearHostOnlyCookie().String(),
		h.buildSessionCookie("", -1).String(),
	}
	return out, nil
}

// clearHostOnlyCookie returns a Set-Cookie value that deletes any ra_session
// cookie stored host-only against api.beelive.ro (no Domain attribute). Used
// alongside buildSessionCookie so both possible cookie scopes get reconciled
// on login and logout.
func clearHostOnlyCookie() *http.Cookie {
	return &http.Cookie{
		Name:     "ra_session",
		Value:    "",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   -1,
	}
}

// buildSessionCookie produces the ra_session cookie used by login, sliding
// renewal, and logout. The Domain attribute comes from COOKIE_DOMAIN (empty
// in dev → host-only cookie). Secure is enabled in production so browsers
// will actually store the cookie on cross-origin HTTPS responses; required
// for the FE on www.beelive.ro to share a cookie set by api.beelive.ro.
func (h *Handlers) buildSessionCookie(token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     "ra_session",
		Value:    token,
		HttpOnly: true,
		Secure:   h.cfg.AppEnv == "production",
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		Domain:   h.cfg.CookieDomain,
		MaxAge:   maxAge,
	}
}

func (h *Handlers) getMe(ctx context.Context, _ *struct{}) (*MeOutput, error) {
	sessionUser := middleware.UserFromContext(ctx)
	if sessionUser == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	user, err := h.authSvc.GetUser(ctx, sessionUser.ID)
	if err != nil {
		return nil, err
	}
	out := &MeOutput{}
	out.Body.User = user
	return out, nil
}
