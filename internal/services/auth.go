package services

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	humaerr "github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"

	"github.com/radarul-albinelor/api/internal/config"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/external/email"
	"github.com/radarul-albinelor/api/internal/platform"
)

type AuthService struct {
	db    *dbsqlc.Queries
	pool  *pgxpool.Pool
	jwt   *platform.JWTService
	email *email.EmailClient
	cfg   *config.Config
}

type LoginResult struct {
	ChallengeID       string `json:"challenge_id"`
	Method            string `json:"method"`
	MaskedDestination string `json:"masked_destination"`
}

func NewAuthService(pool *pgxpool.Pool, jwt *platform.JWTService, emailClient *email.EmailClient, cfg *config.Config) *AuthService {
	sqlDB := stdlib.OpenDBFromPool(pool)
	return &AuthService{
		db:    dbsqlc.New(sqlDB),
		pool:  pool,
		jwt:   jwt,
		email: emailClient,
		cfg:   cfg,
	}
}

func (s *AuthService) Login(ctx context.Context, cnp, password string) (*LoginResult, error) {
	user, err := s.db.GetUserByCNP(ctx, cnp)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, humaerr.NewError(http.StatusUnauthorized, "invalid_credentials")
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, humaerr.NewError(http.StatusUnauthorized, "invalid_credentials")
	}

	method := dbsqlc.AuthMethodSms
	if user.Phone == "" {
		method = dbsqlc.AuthMethodEmail
	}

	code := generateCode()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), 12)
	if err != nil {
		return nil, err
	}

	challengeID := uuid.New()
	if _, err := s.db.CreateChallenge(ctx, dbsqlc.CreateChallengeParams{
		ID:        challengeID,
		UserID:    user.ID,
		Method:    method,
		CodeHash:  string(hash),
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}); err != nil {
		return nil, err
	}

	s.dispatchCode(ctx, user, string(method), code)

	var masked string
	if method == dbsqlc.AuthMethodSms {
		masked = maskedPhone(user.Phone)
	} else {
		masked = maskedEmail(user.Email)
	}
	return &LoginResult{ChallengeID: challengeID.String(), Method: string(method), MaskedDestination: masked}, nil
}

func (s *AuthService) Switch2FAMethod(ctx context.Context, challengeIDStr, method string) (*LoginResult, error) {
	challengeID, err := uuid.Parse(challengeIDStr)
	if err != nil {
		return nil, humaerr.NewError(http.StatusBadRequest, "invalid_challenge_id")
	}
	challenge, err := s.db.GetChallenge(ctx, challengeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, humaerr.NewError(http.StatusNotFound, "challenge_not_found")
		}
		return nil, err
	}
	if challenge.ExpiresAt.Before(time.Now()) {
		return nil, humaerr.NewError(http.StatusBadRequest, "challenge_expired")
	}
	if challenge.VerifiedAt.Valid {
		return nil, humaerr.NewError(http.StatusBadRequest, "already_verified")
	}

	user, err := s.db.GetUserByID(ctx, challenge.UserID)
	if err != nil {
		return nil, err
	}

	dbMethod := dbsqlc.AuthMethod(method)
	code := generateCode()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), 12)
	if err != nil {
		return nil, err
	}
	newID := uuid.New()
	if _, err := s.db.CreateChallenge(ctx, dbsqlc.CreateChallengeParams{
		ID:        newID,
		UserID:    user.ID,
		Method:    dbMethod,
		CodeHash:  string(hash),
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}); err != nil {
		return nil, err
	}
	s.dispatchCode(ctx, user, method, code)

	var masked string
	if method == string(dbsqlc.AuthMethodSms) {
		masked = maskedPhone(user.Phone)
	} else if method == string(dbsqlc.AuthMethodEmail) {
		masked = maskedEmail(user.Email)
	} else {
		masked = user.FullName
	}
	return &LoginResult{ChallengeID: newID.String(), Method: method, MaskedDestination: masked}, nil
}

func (s *AuthService) Verify2FA(ctx context.Context, challengeIDStr, code string) (*domain.User, string, error) {
	challengeID, err := uuid.Parse(challengeIDStr)
	if err != nil {
		return nil, "", humaerr.NewError(http.StatusUnauthorized, "invalid_2fa_code")
	}
	challenge, err := s.db.GetChallenge(ctx, challengeID)
	if err != nil {
		return nil, "", humaerr.NewError(http.StatusUnauthorized, "invalid_2fa_code")
	}
	if challenge.ExpiresAt.Before(time.Now()) || challenge.VerifiedAt.Valid {
		return nil, "", humaerr.NewError(http.StatusUnauthorized, "invalid_2fa_code")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(challenge.CodeHash), []byte(code)); err != nil {
		return nil, "", humaerr.NewError(http.StatusUnauthorized, "invalid_2fa_code")
	}

	if err := s.db.MarkChallengeVerified(ctx, challengeID); err != nil {
		return nil, "", err
	}

	dbUser, err := s.db.GetUserByID(ctx, challenge.UserID)
	if err != nil {
		return nil, "", err
	}
	domainUser := dbUserToDomain(dbUser)
	token, err := s.jwt.Sign(domainUser)
	if err != nil {
		return nil, "", err
	}
	return domainUser, token, nil
}

func (s *AuthService) GetUser(ctx context.Context, userIDStr string) (*domain.User, error) {
	id, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, humaerr.NewError(http.StatusBadRequest, "invalid_user_id")
	}
	dbUser, err := s.db.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, humaerr.NewError(http.StatusNotFound, "user_not_found")
		}
		return nil, err
	}
	return dbUserToDomain(dbUser), nil
}

func (s *AuthService) dispatchCode(ctx context.Context, user dbsqlc.User, method, code string) {
	switch method {
	case string(dbsqlc.AuthMethodPush):
		slog.Info("[2FA PUSH]", "user_id", user.ID, "code", code)
	case string(dbsqlc.AuthMethodSms):
		msg := fmt.Sprintf("Codul dumneavoastră Radarul Albinelor: %s. Expiră în 10 minute.", code)
		if err := sendTwilioSMS(ctx, s.cfg, user.Phone, msg); err != nil {
			slog.Error("SMS dispatch failed", "user_id", user.ID, "err", err)
		}
	case string(dbsqlc.AuthMethodEmail):
		subject := "Cod autentificare Radarul Albinelor"
		body := fmt.Sprintf("Codul dumneavoastră: %s\n\nExpiră în 10 minute.", code)
		if s.email != nil {
			if err := s.email.Send(ctx, user.Email, subject, body); err != nil {
				slog.Info("[2FA EMAIL fallback]", "user_id", user.ID, "code", code, "err", err)
			}
		} else {
			slog.Info("[2FA EMAIL mock]", "user_id", user.ID, "code", code)
		}
	}
}

func sendTwilioSMS(ctx context.Context, cfg *config.Config, to, body string) error {
	if cfg.TwilioAccountSID == "" {
		slog.Info("[MOCK SMS]", "to", to, "body", body)
		return nil
	}
	ctxTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", cfg.TwilioAccountSID)
	form := url.Values{}
	form.Set("From", cfg.TwilioFromPhone)
	form.Set("To", to)
	form.Set("Body", body)
	req, err := http.NewRequestWithContext(ctxTimeout, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(cfg.TwilioAccountSID, cfg.TwilioAuthToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twilio error %d: %s", resp.StatusCode, b)
	}
	return nil
}

func dbUserToDomain(u dbsqlc.User) *domain.User {
	return &domain.User{
		ID:           u.ID.String(),
		CNP:          u.Cnp,
		Name:         u.FullName,
		FullName:     u.FullName,
		Email:        u.Email,
		Phone:        u.Phone,
		Role:         domain.Role(u.Role),
		County:       u.County,
		Locality:     u.Locality,
		PasswordHash: u.PasswordHash,
	}
}

func generateCode() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	n := binary.BigEndian.Uint32(b)
	return fmt.Sprintf("%06d", n%1_000_000)
}

func maskedPhone(phone string) string {
	if len(phone) < 4 {
		return "•••"
	}
	last2 := phone[len(phone)-2:]
	return phone[:4] + "•• ••• •" + last2
}

func maskedEmail(addr string) string {
	parts := strings.SplitN(addr, "@", 2)
	if len(parts) != 2 {
		return "***@***"
	}
	local := parts[0]
	domainPart := parts[1]
	maskedLocal := string(local[0]) + "***"
	domainParts := strings.SplitN(domainPart, ".", 2)
	if len(domainParts) != 2 {
		return maskedLocal + "@***"
	}
	return maskedLocal + "@" + string(domainParts[0][0]) + "***." + domainParts[1]
}
