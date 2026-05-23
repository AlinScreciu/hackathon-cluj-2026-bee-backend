package services

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
)

func Seed(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	q := dbsqlc.New(sqlDB)

	hash, err := bcrypt.GenerateFromPassword([]byte("parola123"), 12)
	if err != nil {
		return err
	}
	h := string(hash)

	users := []dbsqlc.CreateUserParams{
		{ID: uuid.New(), Cnp: "1850101123456", FullName: "Andrei Berar", Email: "andrei.berar@test.com", Phone: "+40721000001", Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Apahida", PasswordHash: h},
		{ID: uuid.New(), Cnp: "2900215654321", FullName: "Maria Costea", Email: "maria.costea@test.com", Phone: "+40721000002", Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Florești", PasswordHash: h},
		{ID: uuid.New(), Cnp: "1780530987654", FullName: "Ioan Lupu", Email: "ioan.lupu@test.com", Phone: "+40721000003", Role: dbsqlc.UserRoleApicultor, County: "Cluj", Locality: "Turda", PasswordHash: h},
		{ID: uuid.New(), Cnp: "1920412111222", FullName: "Vasile Mureșan", Email: "vasile.muresan@test.com", Phone: "+40721000004", Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Câmpia Turzii", PasswordHash: h},
		{ID: uuid.New(), Cnp: "2880721333444", FullName: "Elena Popa", Email: "elena.popa@test.com", Phone: "+40721000005", Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Gherla", PasswordHash: h},
		{ID: uuid.New(), Cnp: "1751103555666", FullName: "Gheorghe Stan", Email: "gheorghe.stan@test.com", Phone: "+40721000006", Role: dbsqlc.UserRoleFermier, County: "Cluj", Locality: "Dej", PasswordHash: h},
		{ID: uuid.New(), Cnp: "1680808777888", FullName: "Inspector Județean Cluj", Email: "inspector@test.com", Phone: "+40721000007", Role: dbsqlc.UserRoleInspector, County: "Cluj", Locality: "Cluj-Napoca", PasswordHash: h},
	}

	seeded := 0
	for _, u := range users {
		existing, err := q.GetUserByCNP(ctx, u.Cnp)
		if err == nil {
			slog.Debug("seed: user exists", "cnp_prefix", u.Cnp[:4], "id", existing.ID)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := q.CreateUser(ctx, u); err != nil {
			return err
		}
		seeded++
		slog.Info("seed: created user", "name", u.FullName, "role", u.Role)
	}
	slog.Info("seed complete", "created", seeded, "total", len(users))
	return nil
}
