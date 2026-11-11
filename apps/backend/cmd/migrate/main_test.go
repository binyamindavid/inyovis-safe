package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalTargetRejectsConnectionOverrides(t *testing.T) {
	tests := []struct {
		raw     string
		allowed bool
	}{
		{"postgres://owner@127.0.0.1:5432/dev?sslmode=disable", true},
		{"postgres://owner@[::1]:5432/dev?sslmode=disable", true},
		{"postgres://owner@remote.example/dev", false},
		{"postgres://owner@localhost/dev", false},
		{"postgres://owner@127.0.0.1/dev?host=remote.example", false},
		{"postgres://owner@127.0.0.1/dev?hostaddr=203.0.113.1", false},
		{"postgres://owner@127.0.0.1/dev?service=remote", false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			parsed, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			err = validateLocalTarget(parsed)
			if (err == nil) != tt.allowed {
				t.Fatalf("validateLocalTarget error = %v; allowed = %t", err, tt.allowed)
			}
		})
	}
}

func TestApplyCommandDoesNotExposeDatabaseURLInArguments(t *testing.T) {
	secret := "postgres://owner:secret@127.0.0.1/dev"
	command := applyCommand(context.Background(), "/tmp/migration-runner", secret, "/tmp/migrations")
	if len(command.Args) != 2 || command.Args[0] != "/tmp/migration-runner" || command.Args[1] != "--apply" {
		t.Fatalf("unexpected child arguments: %q", command.Args)
	}
	if strings.Contains(strings.Join(command.Args, " "), secret) {
		t.Fatal("database URL appeared in child arguments")
	}
}

func TestLatestMigrationDiscoversNewPair(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"000001_first.up.sql", "000001_first.down.sql",
		"000002_second.up.sql", "000002_second.down.sql",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("SELECT 1;"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	version, err := latestMigration(dir)
	if err != nil || version != 2 {
		t.Fatalf("latestMigration = %d, %v; want 2", version, err)
	}
}

func TestLatestMigrationRejectsMissingPair(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"000001_first.up.sql", "000001_first.down.sql", "000002_second.up.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("SELECT 1;"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := latestMigration(dir)
	if err == nil || !strings.Contains(err.Error(), "version 2 needs one up and one down") {
		t.Fatalf("missing pair error = %v", err)
	}
}
