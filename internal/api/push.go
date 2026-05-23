package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerPushPublic(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "get-vapid-public-key",
		Method:      http.MethodGet,
		Path:        "/api/v1/push/vapid-public-key",
		Summary:     "Get VAPID public key",
		Tags:        []string{"push"},
	}, h.getVAPIDPublicKey)
}

func registerPushProtected(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "create-push-subscription",
		Method:      http.MethodPost,
		Path:        "/api/v1/push/subscriptions",
		Summary:     "Register push subscription",
		Tags:        []string{"push"},
	}, h.createPushSubscription)

	huma.Register(api, huma.Operation{
		OperationID: "delete-push-subscription",
		Method:      http.MethodDelete,
		Path:        "/api/v1/push/subscriptions/{id}",
		Summary:     "Remove push subscription",
		Tags:        []string{"push"},
	}, h.deletePushSubscription)
}

func (h *Handlers) getVAPIDPublicKey(_ context.Context, _ *struct{}) (*struct {
	Body struct {
		Key string `json:"key"`
	}
}, error) {
	return &struct {
		Body struct {
			Key string `json:"key"`
		}
	}{Body: struct {
		Key string `json:"key"`
	}{Key: h.cfg.VAPIDPublicKey}}, nil
}

func (h *Handlers) createPushSubscription(_ context.Context, _ *struct{ Body any }) (*struct{ Body any }, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) deletePushSubscription(_ context.Context, _ *struct {
	ID string `path:"id"`
}) (*struct{}, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
