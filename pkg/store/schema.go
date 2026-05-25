package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
	Version int
	Name    string
	SQL     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(e.Name(), "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("bad migration filename %s", e.Name())
		}
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("bad migration version in %s: %w", e.Name(), err)
		}
		b, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{
			Version: v,
			Name:    strings.TrimSuffix(parts[1], ".sql"),
			SQL:     string(b),
		})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms, nil
}

// Migrate applies any migrations whose version > max(schema_migrations.version).
// Each migration file may contain multiple statements separated by ';\n'.
// Safe to call multiple times — already-applied migrations are skipped.
func (s *Store) Migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	var current uint32
	// Ignore error: table may not exist yet on first run; current stays 0.
	_ = s.conn.QueryRow(ctx, `SELECT max(version) FROM skiff.schema_migrations FINAL`).Scan(&current)
	for _, m := range ms {
		if uint32(m.Version) <= current {
			continue
		}
		for _, stmt := range splitStatements(m.SQL) {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if err := s.conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
			}
		}
		if err := s.conn.Exec(ctx,
			`INSERT INTO skiff.schema_migrations (version, description) VALUES (?, ?)`,
			uint32(m.Version), m.Name); err != nil {
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}
	}
	return nil
}

// splitStatements splits a SQL file on ';\n' boundaries.
// ClickHouse driver requires one statement per Exec.
// This split strategy is safe because none of our migrations use ';\n'
// inside enum literals or string values.
func splitStatements(sql string) []string {
	return strings.Split(sql, ";\n")
}
