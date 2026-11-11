package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
