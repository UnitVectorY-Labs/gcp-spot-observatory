package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

const LatestVersion int64 = 1

func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is required (--database-url or SPOT_OBSERVATORY_DATABASE_URL)")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

func VerifySchema(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	version, err := goose.GetDBVersion(db)
	if err != nil {
		return fmt.Errorf("database is not migrated: %w", err)
	}
	if version != LatestVersion {
		return fmt.Errorf("database schema version is %d; expected %d (run migrate)", version, LatestVersion)
	}
	return nil
}
