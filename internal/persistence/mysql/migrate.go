package mysql

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT UNSIGNED PRIMARY KEY, applied_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)) ENGINE=InnoDB`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		version, err := strconv.ParseUint(strings.Split(entry.Name(), "_")[0], 10, 64)
		if err != nil {
			return fmt.Errorf("migration filename %s: %w", entry.Name(), err)
		}
		var exists int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", version).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		raw, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		// The baseline contains its own schema table for clean bootstrap; skip those statements after pre-creating it.
		sqlText := string(raw)
		if version == 1 {
			start := strings.Index(sqlText, "CREATE TABLE departments")
			if start < 0 {
				return fmt.Errorf("invalid baseline migration")
			}
			sqlText = sqlText[start:]
			sqlText = strings.Replace(sqlText, "INSERT INTO schema_migrations(version) VALUES (1);", "", 1)
		}
		for _, statement := range strings.Split(sqlText, ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("migration %d: %w", version, err)
			}
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (?)", version); err != nil {
			return err
		}
	}
	return nil
}
