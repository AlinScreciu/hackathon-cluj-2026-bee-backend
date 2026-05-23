package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerDamage(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "create-damage-claim",
		Method:      http.MethodPost,
		Path:        "/api/v1/damage-claims",
		Summary:     "File a damage claim",
		Tags:        []string{"damage-claims"},
	}, h.createDamageClaim)

	huma.Register(api, huma.Operation{
		OperationID: "list-damage-claims",
		Method:      http.MethodGet,
		Path:        "/api/v1/damage-claims",
		Summary:     "List damage claims",
		Tags:        []string{"damage-claims"},
	}, h.listDamageClaims)

	huma.Register(api, huma.Operation{
		OperationID: "get-damage-claim",
		Method:      http.MethodGet,
		Path:        "/api/v1/damage-claims/{id}",
		Summary:     "Get damage claim",
		Tags:        []string{"damage-claims"},
	}, h.getDamageClaim)

	huma.Register(api, huma.Operation{
		OperationID: "sign-upload",
		Method:      http.MethodPost,
		Path:        "/api/v1/uploads/sign",
		Summary:     "Upload a photo",
		Tags:        []string{"uploads"},
	}, h.signUpload)
}

func (h *Handlers) createDamageClaim(_ context.Context, _ *struct{ Body any }) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) listDamageClaims(_ context.Context, _ *struct{}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) getDamageClaim(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) signUpload(_ context.Context, _ *struct{ Body any }) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
