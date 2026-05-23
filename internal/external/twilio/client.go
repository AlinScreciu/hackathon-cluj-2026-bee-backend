package twilio

import (
	"context"
	"fmt"
	"net/url"

	twilioSDK    "github.com/twilio/twilio-go"
	twilioClient "github.com/twilio/twilio-go/client"
	openapi      "github.com/twilio/twilio-go/rest/api/v2010"
)

type Client struct {
	accountSID string
	authToken  string
	fromPhone  string
	rest       *twilioSDK.RestClient
	validator  twilioClient.RequestValidator
}

func NewClient(accountSID, authToken, fromPhone string) *Client {
	rest := twilioSDK.NewRestClientWithParams(twilioSDK.ClientParams{
		Username: accountSID,
		Password: authToken,
	})
	return &Client{
		accountSID: accountSID,
		authToken:  authToken,
		fromPhone:  fromPhone,
		rest:       rest,
		validator:  twilioClient.NewRequestValidator(authToken),
	}
}

func (c *Client) SendSMS(_ context.Context, to, body string) (string, error) {
	params := &openapi.CreateMessageParams{}
	params.SetTo(to)
	params.SetFrom(c.fromPhone)
	params.SetBody(body)
	msg, err := c.rest.Api.CreateMessage(params)
	if err != nil {
		return "", fmt.Errorf("twilio send sms: %w", err)
	}
	if msg.Sid == nil {
		return "", fmt.Errorf("twilio send sms: nil sid")
	}
	return *msg.Sid, nil
}

func (c *Client) MakeCall(_ context.Context, to, twimlURL string) (string, error) {
	params := &openapi.CreateCallParams{}
	params.SetTo(to)
	params.SetFrom(c.fromPhone)
	params.SetUrl(twimlURL)
	call, err := c.rest.Api.CreateCall(params)
	if err != nil {
		return "", fmt.Errorf("twilio make call: %w", err)
	}
	if call.Sid == nil {
		return "", fmt.Errorf("twilio make call: nil sid")
	}
	return *call.Sid, nil
}

func (c *Client) ValidateSignature(fullURL string, params url.Values, sig string) bool {
	paramsMap := make(map[string]string, len(params))
	for k, v := range params {
		if len(v) > 0 {
			paramsMap[k] = v[0]
		}
	}
	return c.validator.Validate(fullURL, paramsMap, sig)
}
