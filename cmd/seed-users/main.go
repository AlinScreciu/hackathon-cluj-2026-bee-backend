// seed-users seeds prod-side users, parcels, and apiaries against the live DB.
// Idempotent: re-running won't dupe and will reconcile role/phone/name changes.
//
// Layout for the demo trigger story:
//   - Alin Screciu (fermier) owns "Parcela Cluj Centru" at 46.77, 23.59.
//   - Marius Guriță (apicultor) has one apiary inside the 7km radius and one outside.
//   - Alexandra Marian (apicultor) has one apiary inside the 7km radius and one outside.
// When Alin creates a spray on the Cluj Centru parcel, the two "Aproape" apiaries
// receive the alert; the two "Departe" apiaries don't.
//
// Usage:
//
//	DB_CONN_STR="postgres://...neon.tech/...?sslmode=require" \
//	    go run ./cmd/seed-users
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
)

// ── data ────────────────────────────────────────────────────────────────────

type seedUser struct {
	FullName, Email, Phone, County, Locality string
	Role                                     dbsqlc.UserRole
}

type seedParcel struct {
	OwnerEmail, Name, CadastralNumber string
	Lat, Lng, SurfaceHa               float64
	DefaultCrop, County, Locality     string
}

type seedApiary struct {
	OwnerEmail, Name string
	Type             dbsqlc.ApiaryType
	Lat, Lng         float64
	HiveCount        int32
}

var users = []seedUser{
	{FullName: "Marius Guriță", Email: "guritaalex13@gmail.com", Phone: "+40756881589",
		Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Cluj-Napoca"},
	{FullName: "Alin Screciu", Email: "alin.screciu01@gmail.com", Phone: "+40770241335",
		Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Cluj-Napoca"},
	{FullName: "Alexandra Marian", Email: "alexandra0marian@gmail.com", Phone: "+40731390001",
		Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Cluj-Napoca"},
}

var parcels = []seedParcel{
	// Alin's parcel — the trigger anchor at Cluj center.
	{OwnerEmail: "alin.screciu01@gmail.com", Name: "Parcela Cluj Centru",
		CadastralNumber: "CN-CLJ-001", Lat: 46.7700, Lng: 23.5900,
		SurfaceHa: 2.5, DefaultCrop: "rapiță", County: "Cluj", Locality: "Cluj-Napoca"},
}

var apiaries = []seedApiary{
	// Marius — one inside 7 km, one outside.
	{OwnerEmail: "guritaalex13@gmail.com", Name: "Stupina Marius Aproape",
		Type: dbsqlc.ApiaryTypePermanent, Lat: 46.7800, Lng: 23.6200, HiveCount: 12},
	{OwnerEmail: "guritaalex13@gmail.com", Name: "Stupina Marius Departe",
		Type: dbsqlc.ApiaryTypePermanent, Lat: 46.8800, Lng: 23.4500, HiveCount: 8},
	// Alexandra — one inside 7 km, one outside.
	{OwnerEmail: "alexandra0marian@gmail.com", Name: "Stupina Alexandra Aproape",
		Type: dbsqlc.ApiaryTypePermanent, Lat: 46.7500, Lng: 23.5600, HiveCount: 10},
	{OwnerEmail: "alexandra0marian@gmail.com", Name: "Stupina Alexandra Departe",
		Type: dbsqlc.ApiaryTypePermanent, Lat: 46.6500, Lng: 23.7000, HiveCount: 6},
}

// ── main ────────────────────────────────────────────────────────────────────

func main() {
	conn := os.Getenv("DB_CONN_STR")
	if conn == "" {
		fatal("DB_CONN_STR is required")
	}
	password := os.Getenv("SEED_PASSWORD")
	if password == "" {
		password = "parola123"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, conn)
	if err != nil {
		fatal("connect: %v", err)
	}
	defer pool.Close()
	db := stdlib.OpenDBFromPool(pool)
	q := dbsqlc.New(db)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		fatal("bcrypt: %v", err)
	}

	ownerIDByEmail := map[string]uuid.UUID{}
	for _, u := range users {
		id, action, err := upsertUser(ctx, db, q, u, string(hash))
		if err != nil {
			fatal("user %s: %v", u.Email, err)
		}
		ownerIDByEmail[u.Email] = id
		fmt.Printf("user    %-9s %-20s  %s\n", action, u.FullName, id)
	}

	for _, p := range parcels {
		ownerID, ok := ownerIDByEmail[p.OwnerEmail]
		if !ok {
			fatal("parcel %s: owner %s not in seed list", p.Name, p.OwnerEmail)
		}
		action, err := upsertParcel(ctx, db, q, ownerID, p)
		if err != nil {
			fatal("parcel %s: %v", p.Name, err)
		}
		fmt.Printf("parcel  %-9s %-30s  %.4f,%.4f  (%s)\n",
			action, p.Name, p.Lat, p.Lng, p.OwnerEmail)
	}

	for _, a := range apiaries {
		ownerID, ok := ownerIDByEmail[a.OwnerEmail]
		if !ok {
			fatal("apiary %s: owner %s not in seed list", a.Name, a.OwnerEmail)
		}
		action, err := upsertApiary(ctx, db, q, ownerID, a)
		if err != nil {
			fatal("apiary %s: %v", a.Name, err)
		}
		fmt.Printf("apiary  %-9s %-30s  %.4f,%.4f  hives=%d  (%s)\n",
			action, a.Name, a.Lat, a.Lng, a.HiveCount, a.OwnerEmail)
	}

	fmt.Println("\ndone — login with the CNP listed above + 'parola123' + 2FA 000000")
	fmt.Println("note: role changes do not delete orphan parcels/apiaries from a previous role")
}

// ── upserts ─────────────────────────────────────────────────────────────────

func upsertUser(ctx context.Context, db *sql.DB, q *dbsqlc.Queries, u seedUser, passwordHash string) (uuid.UUID, string, error) {
	var (
		existingID                                       uuid.UUID
		curRole                                          string
		curFullName, curPhone, curCounty, curLocality, curCNP string
	)
	err := db.QueryRowContext(ctx,
		`SELECT id, cnp, full_name, phone, role, county, locality FROM users WHERE email = $1 LIMIT 1`,
		u.Email,
	).Scan(&existingID, &curCNP, &curFullName, &curPhone, &curRole, &curCounty, &curLocality)

	switch {
	case err == nil:
		needsUpdate := curRole != string(u.Role) ||
			curFullName != u.FullName ||
			curPhone != u.Phone ||
			curCounty != u.County ||
			curLocality != u.Locality
		if !needsUpdate {
			fmt.Printf("        (cnp=%s role=%s)\n", curCNP, curRole)
			return existingID, "skipped", nil
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE users SET full_name=$1, phone=$2, role=$3, county=$4, locality=$5 WHERE id=$6`,
			u.FullName, u.Phone, u.Role, u.County, u.Locality, existingID,
		); err != nil {
			return uuid.Nil, "", fmt.Errorf("update: %w", err)
		}
		fmt.Printf("        (cnp=%s role: %s -> %s)\n", curCNP, curRole, u.Role)
		return existingID, "updated", nil

	case errors.Is(err, sql.ErrNoRows):
		cnp := randomCNP()
		out, err := q.CreateUser(ctx, dbsqlc.CreateUserParams{
			ID: uuid.New(), Cnp: cnp,
			FullName: u.FullName, Email: u.Email, Phone: u.Phone,
			Role: u.Role, County: u.County, Locality: u.Locality,
			PasswordHash: passwordHash,
		})
		if err != nil {
			return uuid.Nil, "", fmt.Errorf("insert: %w", err)
		}
		fmt.Printf("        (cnp=%s role=%s)\n", cnp, u.Role)
		return out.ID, "created", nil

	default:
		return uuid.Nil, "", fmt.Errorf("select: %w", err)
	}
}

func upsertParcel(ctx context.Context, db *sql.DB, q *dbsqlc.Queries, ownerID uuid.UUID, p seedParcel) (string, error) {
	var existingID uuid.UUID
	err := db.QueryRowContext(ctx,
		`SELECT id FROM parcels WHERE owner_id = $1 AND name = $2 LIMIT 1`,
		ownerID, p.Name,
	).Scan(&existingID)
	if err == nil {
		return "skipped", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("select: %w", err)
	}

	var crop sql.NullString
	if p.DefaultCrop != "" {
		crop = sql.NullString{String: p.DefaultCrop, Valid: true}
	}
	_, err = q.CreateParcel(ctx, dbsqlc.CreateParcelParams{
		ID: uuid.New(), OwnerID: ownerID,
		Name: p.Name, CadastralNumber: p.CadastralNumber,
		Lat: p.Lat, Lng: p.Lng, SurfaceHa: p.SurfaceHa,
		DefaultCrop: crop, County: p.County, Locality: p.Locality,
	})
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}
	return "created", nil
}

func upsertApiary(ctx context.Context, db *sql.DB, q *dbsqlc.Queries, ownerID uuid.UUID, a seedApiary) (string, error) {
	var existingID uuid.UUID
	err := db.QueryRowContext(ctx,
		`SELECT id FROM apiaries WHERE owner_id = $1 AND name = $2 LIMIT 1`,
		ownerID, a.Name,
	).Scan(&existingID)
	if err == nil {
		return "skipped", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("select: %w", err)
	}

	startDate := time.Now().UTC().Truncate(24 * time.Hour)
	_, err = q.CreateApiary(ctx, dbsqlc.CreateApiaryParams{
		ID: uuid.New(), OwnerID: ownerID,
		Name: a.Name, Type: a.Type,
		Lat: a.Lat, Lng: a.Lng,
		HiveCount: a.HiveCount,
		StartDate: startDate,
		EndDate:   sql.NullTime{},
		Notes:     sql.NullString{},
	})
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}
	return "created", nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

// randomCNP returns a 13-digit numeric string with a plausible structure.
// The API does not validate CNP checksums, so this is fine for synthetic accounts.
func randomCNP() string {
	sex := randInt(1, 2)
	year := randInt(80, 99) // 1980-1999
	month := randInt(1, 12)
	day := randInt(1, 28)
	jj := 12 // Cluj
	nnn := randInt(1, 999)
	check := randInt(0, 9)
	return fmt.Sprintf("%d%02d%02d%02d%02d%03d%d", sex, year, month, day, jj, nnn, check)
}

func randInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return int(n.Int64()) + min
}

func fatal(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}
