// seed-users inserts hand-picked real user accounts (beekeeper + farmer) into
// the database. Idempotent by email. Intended for one-off prod seeding against
// Neon (or any Postgres) — pass DB_CONN_STR as env. Password defaults to
// "parola123"; override via SEED_PASSWORD.
//
// Usage:
//
//	DB_CONN_STR="postgres://user:pass@host/db?sslmode=require" \
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

type newUser struct {
	FullName string
	Email    string
	Phone    string
	Role     dbsqlc.UserRole
	County   string
	Locality string
}

var toSeed = []newUser{
	{
		FullName: "Alin Screciu",
		Email:    "alin.screciu01@gmail.com",
		Phone:    "+40770241335",
		Role:     dbsqlc.UserRoleApicultor,
		County:   "Cluj",
		Locality: "Cluj-Napoca",
	},
	{
		FullName: "Alexandra Marian",
		Email:    "alexandra0marian@gmail.com",
		Phone:    "+40731390001",
		Role:     dbsqlc.UserRoleFermier,
		County:   "Cluj",
		Locality: "Cluj-Napoca",
	},
}

func main() {
	conn := os.Getenv("DB_CONN_STR")
	if conn == "" {
		slog.Error("DB_CONN_STR is required")
		os.Exit(1)
	}

	password := os.Getenv("SEED_PASSWORD")
	if password == "" {
		password = "parola123"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, conn)
	if err != nil {
		slog.Error("connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	db := stdlib.OpenDBFromPool(pool)
	q := dbsqlc.New(db)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		slog.Error("bcrypt", "err", err)
		os.Exit(1)
	}

	for _, u := range toSeed {
		var existingCNP string
		err := db.QueryRowContext(ctx, "SELECT cnp FROM users WHERE email = $1 LIMIT 1", u.Email).Scan(&existingCNP)
		if err == nil {
			slog.Info("skip: user already exists", "email", u.Email, "cnp", existingCNP)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("query existing user", "email", u.Email, "err", err)
			os.Exit(1)
		}

		cnp, err := randomCNP(u.Role)
		if err != nil {
			slog.Error("cnp gen", "err", err)
			os.Exit(1)
		}

		_, err = q.CreateUser(ctx, dbsqlc.CreateUserParams{
			ID:           uuid.New(),
			Cnp:          cnp,
			FullName:     u.FullName,
			Email:        u.Email,
			Phone:        u.Phone,
			Role:         u.Role,
			County:       u.County,
			Locality:     u.Locality,
			PasswordHash: string(hash),
		})
		if err != nil {
			slog.Error("insert", "email", u.Email, "err", err)
			os.Exit(1)
		}
		fmt.Printf("created %-20s role=%-9s cnp=%s phone=%s\n", u.FullName, u.Role, cnp, u.Phone)
	}

	fmt.Println("\nlogin: use the CNP above + the password (default 'parola123'). 2FA dev code: 000000")
}

// randomCNP returns a 13-digit numeric string with a plausible structure:
//
//	S(1) YY(2) MM(2) DD(2) JJ(2) NNN(3) C(1)
//
// where S is 1 (M, born <2000) or 2 (F, born <2000), JJ=12 (Cluj). The
// checksum digit is generated random — the API does not validate CNP
// checksums, so this is fine for synthetic accounts.
func randomCNP(role dbsqlc.UserRole) (string, error) {
	_ = role // sex digit isn't tied to role; keep arg for future
	sex := randDigit(1, 2)
	year := randInt(80, 99)   // 1980-1999
	month := randInt(1, 12)
	day := randInt(1, 28)
	jj := 12                  // Cluj
	nnn := randInt(1, 999)
	check := randDigit(0, 9)
	return fmt.Sprintf("%d%02d%02d%02d%02d%03d%d", sex, year, month, day, jj, nnn, check), nil
}

func randInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return int(n.Int64()) + min
}

func randDigit(min, max int) int { return randInt(min, max) }
