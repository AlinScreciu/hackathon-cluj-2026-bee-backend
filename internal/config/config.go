package config

import (
	"github.com/caarlos0/env/v11"
)

type Config struct {
	Port       string `env:"PORT"        envDefault:"8080"`
	AppEnv     string `env:"APP_ENV"     envDefault:"development"`
	AppBaseURL string `env:"APP_BASE_URL" envDefault:"http://localhost:8080"`

	DBConnStr string `env:"DB_CONN_STR" envDefault:"postgres://radarul:radarul@localhost:5433/radarul?sslmode=disable"`

	JWTSecret string `env:"JWT_SECRET" envDefault:"change-me-in-production-32-chars-min"`

	TwilioAccountSID string `env:"TWILIO_ACCOUNT_SID"`
	TwilioAuthToken  string `env:"TWILIO_AUTH_TOKEN"`
	TwilioFromPhone  string `env:"TWILIO_FROM_PHONE"`

	ElevenLabsAPIKey  string `env:"ELEVENLABS_API_KEY"`
	ElevenLabsVoiceID string `env:"ELEVENLABS_VOICE_ID" envDefault:"21m00Tcm4TlvDq8ikWAM"`

	VAPIDPublicKey  string `env:"VAPID_PUBLIC_KEY"`
	VAPIDPrivateKey string `env:"VAPID_PRIVATE_KEY"`

	ResendAPIKey     string `env:"RESEND_API_KEY"`
	ResendFromEmail  string `env:"RESEND_FROM_EMAIL" envDefault:"noreply@beelive.ro"`

	PrimarieEmail string `env:"PRIMARIE_EMAIL" envDefault:"primarie@beelive.ro"`

	GeoAIBaseURL string `env:"GEO_AI_BASE_URL"`

	AllowedOrigins []string `env:"ALLOWED_ORIGINS" envSeparator:"," envDefault:"http://localhost:3000"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
