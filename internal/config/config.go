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

	ElevenLabsAPIKey      string `env:"ELEVENLABS_API_KEY"`
	ElevenLabsVoiceID     string `env:"ELEVENLABS_VOICE_ID"          envDefault:"21m00Tcm4TlvDq8ikWAM"`
	ElevenLabsAgentID     string `env:"ELEVENLABS_AGENT_ID"`
	ElevenLabsPhoneNumID  string `env:"ELEVENLABS_PHONE_NUMBER_ID"`

	VAPIDPublicKey  string `env:"VAPID_PUBLIC_KEY"`
	VAPIDPrivateKey string `env:"VAPID_PRIVATE_KEY"`

	ResendAPIKey     string `env:"RESEND_API_KEY"`
	ResendFromEmail  string `env:"RESEND_FROM_EMAIL" envDefault:"noreply@beelive.ro"`

	PrimarieEmail string `env:"PRIMARIE_EMAIL" envDefault:"primarie@beelive.ro"`

	GeoAIBaseURL string `env:"GEO_AI_BASE_URL"`

	// Cloudflare R2 (for production audio cache). If any are empty, voice
	// MP3s are stored on local disk under ./uploads/voice instead.
	// The bucket is kept private — URLs handed to Twilio are short-lived
	// S3 presigned GETs, so no R2_PUBLIC_BASE_URL is needed.
	R2AccountID       string `env:"R2_ACCOUNT_ID"`
	R2AccessKeyID     string `env:"R2_ACCESS_KEY_ID"`
	R2SecretAccessKey string `env:"R2_SECRET_ACCESS_KEY"`
	R2Bucket          string `env:"R2_BUCKET"`

	AllowedOrigins []string `env:"ALLOWED_ORIGINS" envSeparator:"," envDefault:"http://localhost:3000"`

	// CookieDomain controls the Domain attribute on the ra_session cookie.
	// Empty (default) → host-only cookie, only sent back to the API host.
	// Set to ".beelive.ro" in prod so the cookie is shared across FE/BE
	// subdomains (www.beelive.ro browses, api.beelive.ro authenticates).
	CookieDomain string `env:"COOKIE_DOMAIN"`
}

// R2Enabled reports whether all required R2 settings are present.
func (c *Config) R2Enabled() bool {
	return c.R2AccountID != "" && c.R2AccessKeyID != "" && c.R2SecretAccessKey != "" &&
		c.R2Bucket != ""
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
