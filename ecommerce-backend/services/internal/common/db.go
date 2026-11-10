package common

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/lib/pq"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// OpenDB opens a PostgreSQL connection pool and waits for the server to accept
// connections. This matters in Docker, where services can start before Postgres.
func OpenDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	// Four services plus the worker share one Postgres. The default max_connections is 100,
	// so each service keeps its pool small.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	var pingErr error
	for attempt := 1; attempt <= 30; attempt++ {
		if pingErr = db.Ping(); pingErr == nil {
			return db, nil
		}
		log.Printf("waiting for database (attempt %d/30): %v", attempt, pingErr)
		time.Sleep(time.Second)
	}
	db.Close()
	return nil, fmt.Errorf("database unreachable: %w", pingErr)
}

// IsUniqueViolation reports whether err is a unique-constraint violation.
func IsUniqueViolation(err error) bool { return hasPgCode(err, pgUniqueViolation) }

// IsForeignKeyViolation reports whether err is a foreign-key violation.
func IsForeignKeyViolation(err error) bool { return hasPgCode(err, pgForeignKeyViolation) }

func hasPgCode(err error, code string) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == code
}
