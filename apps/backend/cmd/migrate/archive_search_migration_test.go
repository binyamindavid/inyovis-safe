package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Run migration 16 in a throwaway database so an existing public extension and
// the fresh-install rollback can both be checked without changing test fixtures.
func TestArchiveSearchMigrationExistingUnaccent(t *testing.T) {
	raw := os.Getenv("ARCHIVE_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("disposable PostgreSQL URL required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || validateLocalTarget(parsed) != nil {
		t.Fatal("a direct loopback test database is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	suffix := make([]byte, 6)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "invoyis_search_migration_" + hex.EncodeToString(suffix)
	ident := pgx.Identifier{name}.Sanitize()
	if _, err = owner.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := owner.Exec(context.Background(), "DROP DATABASE "+ident+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	parsed.Path = "/" + name
	scratch, err := pgx.Connect(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer scratch.Close(context.Background())
	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000016_archive_search.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000016_archive_search.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		t.Helper()
		if _, e := scratch.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	schema := func() string {
		t.Helper()
		var got string
		if e := scratch.QueryRow(ctx, `SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='unaccent'`).Scan(&got); e != nil {
			t.Fatal(e)
		}
		return got
	}
	exec("CREATE SCHEMA invoyis")
	exec("CREATE EXTENSION unaccent WITH SCHEMA public")
	exec(string(up))
	if schema() != "public" {
		t.Fatal("migration moved pre-existing extension")
	}
	var folded string
	if err = scratch.QueryRow(ctx, "SELECT invoyis.archive_search_unaccent('Café')").Scan(&folded); err != nil || folded != "Cafe" {
		t.Fatalf("public wrapper %q %v", folded, err)
	}
	var runtimeRole bool
	if err = scratch.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime')").Scan(&runtimeRole); err != nil {
		t.Fatal(err)
	}
	if runtimeRole {
		exec("GRANT USAGE ON SCHEMA invoyis TO invoyis_archive_runtime")
		exec("SET ROLE invoyis_archive_runtime")
		if err = scratch.QueryRow(ctx, "SELECT invoyis.archive_search_unaccent('Café')").Scan(&folded); err != nil || folded != "Cafe" {
			t.Fatalf("restricted public wrapper %q %v", folded, err)
		}
		exec("RESET ROLE")
	}
	exec(string(down))
	if schema() != "public" {
		t.Fatal("rollback removed pre-existing extension")
	}
	exec("DROP EXTENSION unaccent")
	exec(string(up))
	if schema() != "invoyis" {
		t.Fatal("fresh extension not archive scoped")
	}
	exec(string(down))
	var remains bool
	if err = scratch.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='unaccent')").Scan(&remains); err != nil || remains {
		t.Fatalf("fresh extension remains after rollback: %t %v", remains, err)
	}
}
