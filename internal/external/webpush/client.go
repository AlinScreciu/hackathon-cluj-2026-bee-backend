package webpush

import (
	"context"
	"fmt"

	wpLib "github.com/SherClockHolmes/webpush-go"
	"github.com/radarul-albinelor/api/internal/domain"
)

type Client struct {
	vapidPublic  string
	vapidPrivate string
}

func NewClient(vapidPublic, vapidPrivate string) *Client {
	return &Client{vapidPublic: vapidPublic, vapidPrivate: vapidPrivate}
}

func (c *Client) Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error {
	if c.vapidPublic == "" || c.vapidPrivate == "" {
		return nil
	}
	subscription := &wpLib.Subscription{
		Endpoint: sub.Endpoint,
		Keys: wpLib.Keys{
			Auth:   sub.Auth,
			P256dh: sub.P256dh,
		},
	}
	resp, err := wpLib.SendNotificationWithContext(ctx, payload, subscription, &wpLib.Options{
		VAPIDPublicKey:  c.vapidPublic,
		VAPIDPrivateKey: c.vapidPrivate,
		TTL:             30,
		Subscriber:      "mailto:noreply@beelive.ro",
	})
	if err != nil {
		return fmt.Errorf("webpush: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webpush: status %d", resp.StatusCode)
	}
	return nil
}
