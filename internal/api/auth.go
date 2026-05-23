package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
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
		ChallengeID        string `json:"challenge_id"`
		Method             string `json:"method"`
		MaskedDestination  string `json:"masked_destination"`
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
	SetCookie string `header:"Set-Cookie"`
	Body      struct {
		User any `json:"user"`
	}
}

type LogoutOutput struct{}

type MeOutput struct {
	Body struct {
		User any `json:"user"`
	}
}

func (h *Handlers) login(_ context.Context, _ *LoginInput) (*LoginOutput, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) switch2FAMethod(_ context.Context, _ *Switch2FAInput) (*LoginOutput, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) verify2FA(_ context.Context, _ *Verify2FAInput) (*Verify2FAOutput, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) logout(_ context.Context, _ *struct{}) (*LogoutOutput, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getMe(_ context.Context, _ *struct{}) (*MeOutput, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
