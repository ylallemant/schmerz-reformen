// Package store opens the database and keeps its schema up to date.
//
// PostgreSQL in production, SQLite in development, both through GORM — which
// is why nothing here uses engine-specific SQL. The SQLite driver is pure Go,
// so the whole project still builds with CGO_ENABLED=0 and ships in a static,
// shell-less image.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/rs/zerolog/log"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Driver names the database engine.
type Driver string

const (
	// DriverPostgres is the production engine.
	DriverPostgres Driver = "postgres"
	// DriverSQLite is the development engine.
	DriverSQLite Driver = "sqlite"
)

// Valid reports whether the driver is one we support.
func (d Driver) Valid() bool {
	return d == DriverPostgres || d == DriverSQLite
}

// Options configures the database connection.
type Options struct {
	Driver Driver

	// DSN is the connection string: a PostgreSQL URL, or a SQLite file path.
	DSN string

	// MaxOpenConns and MaxIdleConns bound the pool. Zero uses the defaults.
	MaxOpenConns int
	MaxIdleConns int
}

// Store is the database handle.
type Store struct {
	db     *gorm.DB
	driver Driver
}

// Open connects and verifies the connection works before returning it.
func Open(opts Options) (*Store, error) {
	if !opts.Driver.Valid() {
		return nil, fmt.Errorf("unknown database driver %q: want %s or %s",
			opts.Driver, DriverPostgres, DriverSQLite)
	}
	if opts.DSN == "" {
		return nil, fmt.Errorf("database dsn is required for driver %q", opts.Driver)
	}

	var dialector gorm.Dialector
	switch opts.Driver {
	case DriverPostgres:
		dialector = postgres.Open(opts.DSN)
	case DriverSQLite:
		dialector = sqlite.Open(opts.DSN)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: gormLogger(),
		// Identifiers are assigned in Go, so GORM never needs to read one back.
		SkipDefaultTransaction: false,
	})
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", opts.Driver, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("access connection pool: %w", err)
	}
	if opts.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping %s database: %w", opts.Driver, err)
	}

	log.Info().Str("driver", string(opts.Driver)).Msg("database connected")
	return &Store{db: db, driver: opts.Driver}, nil
}

// DB is the GORM handle.
func (s *Store) DB() *gorm.DB { return s.db }

// Driver reports the engine in use.
func (s *Store) Driver() Driver { return s.driver }

// Migrate brings the schema up to date. It must produce the same result on
// both engines, which is what makes the development database trustworthy.
func (s *Store) Migrate() error {
	if err := s.db.AutoMigrate(schema()...); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	if err := migrateLegacyMembers(s.db); err != nil {
		return fmt.Errorf("move member organisations into their own table: %w", err)
	}
	log.Info().Msg("database schema up to date")
	return nil
}

// schema lists every persisted type, in dependency order.
func schema() []any {
	return []any{
		&models.Collective{},
		&models.Organisation{},
		&models.OrganisationChange{},
		&models.ChangeVote{},
		&models.CollectiveMember{},
		&models.Media{},
		&models.Topic{},
		&models.TopicUpdate{},
		&models.Action{},
		&models.Account{},
		&models.Credential{},
		&models.RecoveryCode{},
		&models.WebAuthnCeremony{},
		&models.LinkToken{},
		&models.Session{},
		&models.PushSubscription{},
		&models.Notification{},
		&models.Follow{},
		&models.Participation{},
		&models.AuditEntry{},
		&models.ThemeFile{},
		&models.ThemeColor{},
		&models.ThemeAsset{},
	}
}

// Close releases the connection pool.
func (s *Store) Close(context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("access connection pool: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	log.Info().Msg("database closed")
	return nil
}

// Ready reports whether the database is reachable, for the readiness probe.
func (s *Store) Ready(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

// gormLogger routes GORM's own output through zerolog. Queries are
// high-frequency, so they belong at trace, never at info.
func gormLogger() logger.Interface {
	return logger.New(&zerologWriter{}, logger.Config{
		SlowThreshold:             500 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

type zerologWriter struct{}

func (zerologWriter) Printf(format string, args ...any) {
	log.Warn().Msgf(format, args...)
}
