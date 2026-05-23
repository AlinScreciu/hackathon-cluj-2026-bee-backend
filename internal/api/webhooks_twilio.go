package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerTwilioWebhooks(api huma.API, h *Handlers) {
	huma.Register(api, huma.Operation{
		OperationID: "twilio-voice-status",
		Method:      http.MethodPost,
		Path:        "/api/v1/webhooks/twilio/voice/status",
		Summary:     "Twilio voice status callback",
		Tags:        []string{"webhooks"},
	}, h.twilioVoiceStatus)

	huma.Register(api, huma.Operation{
		OperationID: "twilio-voice-gather",
		Method:      http.MethodPost,
		Path:        "/api/v1/webhooks/twilio/voice/gather",
		Summary:     "Twilio voice DTMF gather",
		Tags:        []string{"webhooks"},
	}, h.twilioVoiceGather)

	huma.Register(api, huma.Operation{
		OperationID: "twilio-sms-inbound",
		Method:      http.MethodPost,
		Path:        "/api/v1/webhooks/twilio/sms/inbound",
		Summary:     "Twilio inbound SMS",
		Tags:        []string{"webhooks"},
	}, h.twilioSMSInbound)

	huma.Register(api, huma.Operation{
		OperationID: "twilio-sms-status",
		Method:      http.MethodPost,
		Path:        "/api/v1/webhooks/twilio/sms/status",
		Summary:     "Twilio SMS status callback",
		Tags:        []string{"webhooks"},
	}, h.twilioSMSStatus)
}

func (h *Handlers) twilioVoiceStatus(_ context.Context, _ *struct{ Body any }) (*struct{}, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) twilioVoiceGather(_ context.Context, _ *struct{ Body any }) (*struct{}, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) twilioSMSInbound(_ context.Context, _ *struct{ Body any }) (*struct{}, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}

func (h *Handlers) twilioSMSStatus(_ context.Context, _ *struct{ Body any }) (*struct{}, error) {
	return nil, huma.NewError(http.StatusNotImplemented, "not implemented")
}
