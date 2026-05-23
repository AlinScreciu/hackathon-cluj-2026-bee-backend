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

func NewClient(host string, port int, user, password, from string) *EmailClient {
	return &EmailClient{host: host, port: port, user: user, password: password, from: from}
}

func (c *EmailClient) Send(ctx context.Context, to, subject, body string) error {
	client, err := mail.NewClient(c.host,
		mail.WithPort(c.port),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
		mail.WithUsername(c.user),
		mail.WithPassword(c.password),
		mail.WithTLSConfig(&tls.Config{InsecureSkipVerify: false}),
		mail.WithSSL(),
	)
	if err != nil {
		return err
	}
	m := mail.NewMsg()
	if err := m.From(c.from); err != nil {
		return err
	}
	if err := m.To(to); err != nil {
		return err
	}
	m.Subject(subject)
	m.SetBodyString(mail.TypeTextPlain, body)
	return client.DialAndSendWithContext(ctx, m)
}
