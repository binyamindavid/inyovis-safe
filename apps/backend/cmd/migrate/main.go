// Command migrate runs the pinned golang-migrate library after a serialized safety check.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	migrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
)

var migrationName = regexp.MustCompile(`^(\d+)_.+\.(up|down)\.sql$`)

func main() {
	var err error
	if len(os.Args) == 2 && os.Args[1] == "--apply" {
		err = apply()
	} else {
		err = run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "migration failed:", err)
		os.Exit(1)
	}
}

func run() error {
	dbURL := os.Getenv("MIGRATION_DATABASE_URL")
	if dbURL == "" {
		return errors.New("set MIGRATION_DATABASE_URL to the migration-owner PostgreSQL URL")
	}
	migrations := os.Getenv("MIGRATIONS_PATH")
	if migrations == "" {
		migrations = "migrations"
	}
	path, err := filepath.Abs(migrations)
	if err != nil {
		return err
	}
	latest, err := latestMigration(path)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(dbURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return errors.New("MIGRATION_DATABASE_URL must be a PostgreSQL URL")
	}
	if os.Getenv("MIGRATION_LOCAL_ONLY") == "1" {
		if err := validateLocalTarget(parsed); err != nil {
			return err
		}
	}
	query := parsed.Query()
	query.Set("search_path", "public")
	parsed.RawQuery = query.Encode()
	dbURL = parsed.String()
	jobTimeout, err := durationEnv("MIGRATION_JOB_TIMEOUT", 5*time.Minute)
	if err != nil {
		return err
	}
	lockTimeout, err := durationEnv("MIGRATION_LOCK_TIMEOUT", 30*time.Second)
	if err != nil {
		return err
	}
	if lockTimeout < time.Second {
		return errors.New("MIGRATION_LOCK_TIMEOUT must be at least 1s")
	}
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return errors.New("cannot connect as migration owner; check database availability and credentials")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	lockDeadline := time.Now().Add(lockTimeout)
	for {
		var locked bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(740038, 1)`).Scan(&locked); err != nil {
			return fmt.Errorf("cannot acquire migration guard lock: %w", err)
		}
		if locked {
			break
		}
		if time.Now().After(lockDeadline) {
			return fmt.Errorf("migration guard lock timed out after %s; another runner may be active", lockTimeout)
		}
		select {
		case <-ctx.Done():
			return errors.New("migration job timed out while waiting for lock")
		case <-time.After(200 * time.Millisecond):
		}
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(740038, 1)`)
	}()
	start, dirty, err := ledgerVersion(ctx, conn)
	if err != nil {
		return err
	}
	fmt.Printf("Migration start: version %s; latest file: %d\n", start, latest)
	if dirty {
		return errors.New("ledger is dirty; inspect the failed migration before an explicit repair")
	}
	if start != "none" {
		version, _ := strconv.Atoi(start)
		if version > latest {
			return fmt.Errorf("ledger version %d is newer than latest migration file %d; use matching code", version, latest)
		}
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find migration runner: %w", err)
	}
	command := applyCommand(ctx, self, dbURL, path)
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("migration job exceeded %s; inspect the ledger before retrying", jobTimeout)
		}
		message := strings.TrimSpace(string(output))
		if strings.Contains(message, "://") || strings.Contains(strings.ToLower(message), "password=") {
			message = "migration command failed; inspect the SQL and ledger"
		}
		message = strings.ReplaceAll(message, dbURL, "[database URL]")
		if parsed.User != nil {
			if password, ok := parsed.User.Password(); ok && password != "" {
				message = strings.ReplaceAll(message, password, "[database password]")
			}
			message = strings.ReplaceAll(message, parsed.User.String(), "[database user]")
		}
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("golang-migrate: %s; inspect public.schema_migrations before retrying", message)
	}
	end, dirty, err := ledgerVersion(ctx, conn)
	if err != nil {
		return err
	}
	if dirty || end != strconv.Itoa(latest) {
		return fmt.Errorf("unexpected ending ledger version %s dirty=%t; inspect before release", end, dirty)
	}
	fmt.Printf("Migration end: version %s\n", end)
	return nil
}

func applyCommand(ctx context.Context, executable, dbURL, path string) *exec.Cmd {
	command := exec.CommandContext(ctx, executable, "--apply")
	command.Env = append(os.Environ(), "MIGRATION_DATABASE_URL="+dbURL, "MIGRATIONS_PATH="+path)
	return command
}

func validateLocalTarget(parsed *url.URL) error {
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return errors.New("MIGRATION_DATABASE_URL must be a PostgreSQL URL")
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	if err != nil || !address.IsLoopback() {
		return errors.New("local migrations require a loopback IP address in DATABASE_URL (127.0.0.1 or ::1)")
	}
	for key := range parsed.Query() {
		switch strings.ToLower(key) {
		case "sslmode", "connect_timeout", "search_path":
		default:
			return fmt.Errorf("local migrations reject connection option %q; use a direct loopback PostgreSQL URL", key)
		}
	}
	return nil
}

func apply() error {
	dbURL := os.Getenv("MIGRATION_DATABASE_URL")
	path := os.Getenv("MIGRATIONS_PATH")
	if dbURL == "" || path == "" {
		return errors.New("migration runner is missing its database URL or SQL directory")
	}
	if os.Getenv("MIGRATION_LOCAL_ONLY") == "1" {
		parsed, err := url.Parse(dbURL)
		if err != nil {
			return errors.New("MIGRATION_DATABASE_URL must be a PostgreSQL URL")
		}
		if err := validateLocalTarget(parsed); err != nil {
			return err
		}
	}
	lockTimeout, err := durationEnv("MIGRATION_LOCK_TIMEOUT", 30*time.Second)
	if err != nil {
		return err
	}
	m, err := migrate.New("file://"+path, dbURL)
	if err != nil {
		return err
	}
	defer m.Close()
	m.LockTimeout = lockTimeout
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	if value := os.Getenv(name); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 || duration > 10*time.Minute {
			return 0, fmt.Errorf("%s must be a positive duration at most 10m", name)
		}
		return duration, nil
	}
	return fallback, nil
}

func latestMigration(path string) (int, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, fmt.Errorf("read migration directory: %w", err)
	}
	files := map[int]uint8{}
	latest := 0
	for _, entry := range entries {
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.Atoi(match[1])
		if err != nil || version < 1 {
			return 0, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		bit := uint8(1)
		if match[2] == "down" {
			bit = 2
		}
		if files[version]&bit != 0 {
			return 0, fmt.Errorf("duplicate migration version %d", version)
		}
		files[version] |= bit
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, errors.New("migration directory has no SQL files")
	}
	for version := 1; version <= latest; version++ {
		if files[version] != 3 {
			return 0, fmt.Errorf("migration version %d needs one up and one down SQL file", version)
		}
	}
	return latest, nil
}

func ledgerVersion(ctx context.Context, conn *pgx.Conn) (string, bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return "", false, err
	}
	if !exists {
		var populated bool
		if err := conn.QueryRow(ctx, `SELECT to_regnamespace('invoyis') IS NOT NULL`).Scan(&populated); err != nil {
			return "", false, err
		}
		if populated {
			return "", false, errors.New("Invoyis schema exists without public.schema_migrations; inspect and baseline explicitly")
		}
		return "none", false, nil
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.schema_migrations`).Scan(&count); err != nil {
		return "", false, fmt.Errorf("cannot read public.schema_migrations ledger: %w", err)
	}
	var schemaExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regnamespace('invoyis') IS NOT NULL`).Scan(&schemaExists); err != nil {
		return "", false, err
	}
	if count == 0 && !schemaExists {
		return "none", false, nil
	}
	if count != 1 {
		return "", false, fmt.Errorf("public.schema_migrations has %d rows; inspect its history before migrating", count)
	}
	if !schemaExists {
		return "", false, errors.New("public.schema_migrations exists but Invoyis schema is missing; inspect the database before migrating")
	}
	var version int
	var dirty bool
	err := conn.QueryRow(ctx, `SELECT version, dirty FROM public.schema_migrations`).Scan(&version, &dirty)
	if err != nil {
		return "", false, fmt.Errorf("invalid public.schema_migrations ledger: %w", err)
	}
	if version < 1 {
		return "", false, fmt.Errorf("invalid ledger version %d", version)
	}
	return strconv.Itoa(version), dirty, nil
}
