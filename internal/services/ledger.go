package services

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
	"github.com/radarul-albinelor/api/internal/domain"
)

type LedgerService struct {
	pool  *pgxpool.Pool
	sqlDB *sql.DB
}

type LedgerChain struct {
	PrevHash *string `json:"prev_hash"`
	NextHash *string `json:"next_hash"`
}

type VerifyResult struct {
	Valid       bool      `json:"valid"`
	TotalEvents int       `json:"total_events"`
	LastHash    string    `json:"last_hash"`
	CheckedAt   time.Time `json:"checked_at"`
	Error       string    `json:"error,omitempty"`
}

func NewLedgerService(pool *pgxpool.Pool) *LedgerService {
	return &LedgerService{
		pool:  pool,
		sqlDB: stdlib.OpenDBFromPool(pool),
	}
}

// Append adds a new event to the ledger hash chain.
// If tx is non-nil, Append participates in the caller's transaction.
// If tx is nil, Append manages its own transaction.
// Returns the new event's hash.
func (s *LedgerService) Append(ctx context.Context, tx *sql.Tx, eventType string, actorID *string, payload any) (string, error) {
	ownTx := tx == nil
	if ownTx {
		var err error
		tx, err = s.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return "", fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback()
	}

	// Serialize all appends with an advisory lock scoped to this transaction.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(42)"); err != nil {
		return "", fmt.Errorf("advisory lock: %w", err)
	}

	q := dbsqlc.New(tx)

	prevHash := ""
	last, err := q.GetLastLedgerEvent(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("get last event: %w", err)
	}
	if err == nil {
		prevHash = last.Hash
	}

	canonical, err := canonicalJSON(payload)
	if err != nil {
		return "", fmt.Errorf("canonical json: %w", err)
	}

	createdAt := time.Now().UTC()
	hash := sha256hex(prevHash + canonical + createdAt.Format(time.RFC3339))

	var prevHashNull sql.NullString
	if prevHash != "" {
		prevHashNull = sql.NullString{String: prevHash, Valid: true}
	}

	var actorIDNull uuid.NullUUID
	if actorID != nil {
		if parsed, parseErr := uuid.Parse(*actorID); parseErr == nil {
			actorIDNull = uuid.NullUUID{UUID: parsed, Valid: true}
		}
	}

	payloadJSON, _ := json.Marshal(payload)

	if _, err = q.InsertLedgerEvent(ctx, dbsqlc.InsertLedgerEventParams{
		ID:        uuid.New(),
		Hash:      hash,
		PrevHash:  prevHashNull,
		Type:      eventType,
		ActorID:   actorIDNull,
		Payload:   json.RawMessage(payloadJSON),
		CreatedAt: createdAt,
	}); err != nil {
		return "", fmt.Errorf("insert event: %w", err)
	}

	if ownTx {
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit: %w", err)
		}
	}

	slog.Debug("ledger event appended", "type", eventType, "hash", hash[:8])
	return hash, nil
}

// List returns paginated ledger events with optional type and actor filters.
func (s *LedgerService) List(ctx context.Context, typeFilter, actorFilter string, limit, offset int) ([]domain.LedgerEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	q := dbsqlc.New(s.sqlDB)

	var rows []dbsqlc.LedgerEvent
	var err error

	if actorFilter != "" {
		actorUUID, parseErr := uuid.Parse(actorFilter)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid actor UUID: %w", parseErr)
		}
		rows, err = q.ListLedgerEventsPaginated(ctx, dbsqlc.ListLedgerEventsPaginatedParams{
			Column1: typeFilter,
			Column2: actorUUID,
			Limit:   int32(limit),
			Offset:  int32(offset),
		})
	} else {
		// Use ListLedgerEventsByType to avoid the zero-UUID false-negative in ListLedgerEventsPaginated.
		rows, err = q.ListLedgerEventsByType(ctx, dbsqlc.ListLedgerEventsByTypeParams{
			Column1: typeFilter,
			Limit:   int32(limit),
			Offset:  int32(offset),
		})
	}

	if err != nil {
		return nil, err
	}

	result := make([]domain.LedgerEvent, len(rows))
	for i, r := range rows {
		result[i] = dbLedgerToDomain(r)
	}
	return result, nil
}

// ListByApiaryID returns all ledger events whose payload references the given apiary_id,
// ordered chronologically (oldest first). Returns an empty slice when no events exist.
func (s *LedgerService) ListByApiaryID(ctx context.Context, apiaryID string) ([]domain.LedgerEvent, error) {
	q := dbsqlc.New(s.sqlDB)
	rows, err := q.ListLedgerEventsByApiaryID(ctx, apiaryID)
	if err != nil {
		return nil, err
	}

	result := make([]domain.LedgerEvent, len(rows))
	for i, r := range rows {
		result[i] = dbLedgerToDomain(r)
	}
	return result, nil
}

// ListBySprayID returns all ledger events whose payload references the given spray_id,
// ordered chronologically. Used to render the per-spray audit trail on the spray
// detail page (spray.created, alert.dispatched, pdf.generated, email.sent, …).
func (s *LedgerService) ListBySprayID(ctx context.Context, sprayID string) ([]domain.LedgerEvent, error) {
	q := dbsqlc.New(s.sqlDB)
	rows, err := q.ListLedgerEventsBySprayID(ctx, sprayID)
	if err != nil {
		return nil, err
	}

	result := make([]domain.LedgerEvent, len(rows))
	for i, r := range rows {
		result[i] = dbLedgerToDomain(r)
	}
	return result, nil
}

// ListByActorID returns all ledger events authored by the given actor (user ID),
// ordered chronologically. Used to render the per-farmer activity trail on the
// inspector farmer-detail page.
func (s *LedgerService) ListByActorID(ctx context.Context, actorID string) ([]domain.LedgerEvent, error) {
	actorUUID, err := uuid.Parse(actorID)
	if err != nil {
		return nil, fmt.Errorf("invalid actor UUID: %w", err)
	}
	q := dbsqlc.New(s.sqlDB)
	rows, err := q.ListLedgerEventsByActorID(ctx, uuid.NullUUID{UUID: actorUUID, Valid: true})
	if err != nil {
		return nil, err
	}

	result := make([]domain.LedgerEvent, len(rows))
	for i, r := range rows {
		result[i] = dbLedgerToDomain(r)
	}
	return result, nil
}

// GetByHash returns a single event by hash along with its chain context (prev/next hashes).
// Returns nil, nil, nil when the hash is not found (caller should return 404).
func (s *LedgerService) GetByHash(ctx context.Context, hash string) (*domain.LedgerEvent, *LedgerChain, error) {
	q := dbsqlc.New(s.sqlDB)

	row, err := q.GetLedgerEventByHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	e := dbLedgerToDomain(row)
	chain := &LedgerChain{}
	if row.PrevHash.Valid {
		chain.PrevHash = &row.PrevHash.String
	}

	next, nextErr := q.GetNextLedgerEvent(ctx, sql.NullString{String: hash, Valid: true})
	if nextErr == nil {
		chain.NextHash = &next.Hash
	}

	return &e, chain, nil
}

// Verify walks all events in chronological order and recomputes each hash to detect tampering.
func (s *LedgerService) Verify(ctx context.Context) (*VerifyResult, error) {
	q := dbsqlc.New(s.sqlDB)
	events, err := q.ListLedgerEventsOrdered(ctx)
	if err != nil {
		return nil, err
	}

	result := &VerifyResult{
		Valid:       true,
		TotalEvents: len(events),
		CheckedAt:   time.Now().UTC(),
	}

	prevHash := ""
	for _, e := range events {
		expectedPrev := ""
		if e.PrevHash.Valid {
			expectedPrev = e.PrevHash.String
		}
		if expectedPrev != prevHash {
			result.Valid = false
			result.Error = fmt.Sprintf("chain broken at event %s: expected prev=%q got=%q", e.ID, prevHash, expectedPrev)
			return result, nil
		}

		canonical, canonErr := canonicalJSON(e.Payload)
		if canonErr != nil {
			return nil, canonErr
		}
		expected := sha256hex(prevHash + canonical + e.CreatedAt.UTC().Format(time.RFC3339))
		if expected != e.Hash {
			result.Valid = false
			result.Error = fmt.Sprintf("hash mismatch at event %s: expected prefix %s got %s", e.ID, expected[:8], e.Hash[:8])
			return result, nil
		}
		prevHash = e.Hash
	}

	result.LastHash = prevHash
	return result, nil
}

func canonicalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var m any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	b2, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b2), nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)
}

func dbLedgerToDomain(e dbsqlc.LedgerEvent) domain.LedgerEvent {
	var prevHash *string
	if e.PrevHash.Valid {
		prevHash = &e.PrevHash.String
	}
	var actorID *string
	if e.ActorID.Valid {
		s := e.ActorID.UUID.String()
		actorID = &s
	}
	var payload map[string]any
	_ = json.Unmarshal(e.Payload, &payload)
	return domain.LedgerEvent{
		ID:        e.ID.String(),
		Hash:      e.Hash,
		PrevHash:  prevHash,
		Type:      e.Type,
		ActorID:   actorID,
		Payload:   payload,
		CreatedAt: e.CreatedAt,
	}
}
