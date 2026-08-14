// Package migrate applies the queue schema migrations to PostgreSQL.
//
// Migrations are tracked in a schema_migrations table and applied
// transactionally, one file at a time, in lexicographic order. Apply is
// idempotent and safe to call on every startup.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/RenderginGen/queue/migrations"
)

// Apply runs all pending migrations against db. A migration is considered
// applied when its filename is recorded in schema_migrations.
func Apply(ctx context.Context, db *sql.DB) error {
	if err := ensureLedger(ctx, db); err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	names, err := migrations.Names()
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}

	for _, name := range names {
		if applied[name] {
			continue
		}
		if err := applyOne(ctx, db, name); err != nil {
			return err
		}
	}
	return nil
}

// ensureLedger creates the migration ledger table if it does not exist.
func ensureLedger(ctx context.Context, db *sql.DB) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// appliedVersions returns the set of migration filenames already applied.
func appliedVersions(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// applyOne applies a single migration file inside a transaction and records it
// in the ledger only after the DDL succeeds.
func applyOne(ctx context.Context, db *sql.DB, name string) error {
	script, err := migrations.FS.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback()

	for _, stmt := range splitStatements(string(script)) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// splitStatements splits a SQL script into individual statements on
// semicolons that fall outside string literals and line comments. It exists
// because PostgreSQL's extended query protocol does not accept multiple
// statements in a single Exec. Block comments and dollar-quoted strings are
// not supported because the shipped migrations do not use them.
func splitStatements(script string) []string {
	var out []string
	var b strings.Builder

	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// Line comment: skip to end of line so a semicolon inside a comment
		// does not terminate the statement.
		if r == '-' && i+1 < len(runes) && runes[i+1] == '-' {
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			continue
		}

		// String literal: copy verbatim so a semicolon inside a literal is
		// not treated as a statement boundary. Doubled quotes are escaped.
		if r == '\'' {
			b.WriteRune(r)
			for i+1 < len(runes) {
				i++
				b.WriteRune(runes[i])
				if runes[i] == '\'' {
					if i+1 < len(runes) && runes[i+1] == '\'' {
						i++
						b.WriteRune(runes[i])
						continue
					}
					break
				}
			}
			continue
		}

		if r == ';' {
			if stmt := strings.TrimSpace(b.String()); stmt != "" {
				out = append(out, stmt)
			}
			b.Reset()
			continue
		}

		b.WriteRune(r)
	}

	if stmt := strings.TrimSpace(b.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}
