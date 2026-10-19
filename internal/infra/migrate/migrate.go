package migrate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/TheProzin/Jungle-Go/migrations"
)

func Up(databaseURL string) error {
	instance, err := newInstance(databaseURL)
	if err != nil {
		return err
	}
	defer instance.Close()

	if err := instance.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func Down(databaseURL string, steps int) error {
	if steps < 1 {
		return fmt.Errorf("steps must be greater than zero")
	}

	instance, err := newInstance(databaseURL)
	if err != nil {
		return err
	}
	defer instance.Close()

	if err := instance.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("revert %d migration(s): %w", steps, err)
	}
	return nil
}

func Version(databaseURL string) (uint, bool, error) {
	instance, err := newInstance(databaseURL)
	if err != nil {
		return 0, false, err
	}
	defer instance.Close()

	version, dirty, err := instance.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, dirty, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read migration version: %w", err)
	}
	return version, dirty, nil
}

func newInstance(databaseURL string) (*migrate.Migrate, error) {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations: %w", err)
	}

	instance, err := migrate.NewWithSourceInstance("iofs", source, databaseURLForDriver(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("create migration instance: %w", err)
	}
	return instance, nil
}

func databaseURLForDriver(databaseURL string) string {
	switch {
	case strings.HasPrefix(databaseURL, "pgx5://"):
		return databaseURL
	case strings.HasPrefix(databaseURL, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgres://")
	case strings.HasPrefix(databaseURL, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgresql://")
	default:
		return databaseURL
	}
}
