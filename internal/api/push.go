package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/middleware"
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

// CreatePushSubInput holds the body for registering a web push subscription.
type CreatePushSubInput struct {
	Body struct {
		Endpoint string `json:"endpoint"`
		// ExpirationTime is part of the browser PushSubscription.toJSON() shape;
		// browsers always emit it (usually null). We accept and ignore it.
		ExpirationTime *int64 `json:"expirationTime,omitempty"`
		Keys           struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
}

func (h *Handlers) createPushSubscription(ctx context.Context, input *CreatePushSubInput) (*struct {
	Body struct {
		ID string `json:"id"`
	}
}, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}
	row, err := q.CreatePushSubscription(ctx, dbsqlc.CreatePushSubscriptionParams{
		ID:       uuid.New(),
		UserID:   userID,
		Endpoint: input.Body.Endpoint,
		P256dh:   input.Body.Keys.P256dh,
		Auth:     input.Body.Keys.Auth,
	})
	if err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	out := &struct {
		Body struct {
			ID string `json:"id"`
		}
	}{}
	out.Body.ID = row.ID.String()
	return out, nil
}

func (h *Handlers) deletePushSubscription(ctx context.Context, input *struct {
	ID string `path:"id"`
}) (*struct{}, error) {
	user := middleware.UserFromContext(ctx)
	if user == nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}

	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "ID invalid")
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return nil, huma.NewError(http.StatusUnauthorized, "Sesiune invalidă sau expirată")
	}

	sqlDB := stdlib.OpenDBFromPool(h.pool)
	q := dbsqlc.New(sqlDB)

	if err := q.DeletePushSubscription(ctx, dbsqlc.DeletePushSubscriptionParams{
		ID:     id,
		UserID: userID,
	}); err != nil {
		return nil, huma.NewError(http.StatusInternalServerError, "Eroare internă")
	}

	return &struct{}{}, nil
}
