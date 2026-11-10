package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Storage interface {
	Read(context.Context, string, int64) ([]byte, error)
}
type Scanner func(context.Context, []byte) (bool, error)
type Parser func(context.Context, []byte) (Result, error)
type Result struct {
	Profile    string         `json:"profile,omitempty"`
	Candidates map[string]any `json:"candidates"`
	Provenance map[string]any `json:"provenance"`
	Warnings   []string       `json:"warnings"`
}
type Config struct {
	MaxClaims       int
	MaxParserStarts int
	Lease           time.Duration
	Deadline        time.Duration
	BackoffBase     time.Duration
	BackoffCap      time.Duration
	Poll            time.Duration
	MaxBytes        int64
}

func DefaultConfig() Config {
	return Config{5, 2, 90 * time.Second, 5 * time.Minute, 5 * time.Second, 5 * time.Minute, time.Second, 25 << 20}
}
func ConfigFromEnv() (Config, error) {
	c := DefaultConfig()
	ints := []struct {
		name     string
		dest     *int
		min, max int
	}{{"WORKER_MAX_CLAIMS", &c.MaxClaims, 1, 100}, {"WORKER_MAX_PARSER_STARTS", &c.MaxParserStarts, 1, 100}}
	for _, v := range ints {
		if raw := os.Getenv(v.name); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < v.min || n > v.max {
				return c, fmt.Errorf("invalid %s", v.name)
			}
			*v.dest = n
		}
	}
	durations := []struct {
		name     string
		dest     *time.Duration
		min, max time.Duration
	}{{"WORKER_LEASE", &c.Lease, 3 * time.Second, time.Hour}, {"WORKER_DEADLINE", &c.Deadline, 5 * time.Second, 2 * time.Hour}, {"WORKER_BACKOFF_BASE", &c.BackoffBase, time.Second, time.Hour}, {"WORKER_BACKOFF_CAP", &c.BackoffCap, time.Second, 24 * time.Hour}, {"WORKER_POLL", &c.Poll, 100 * time.Millisecond, time.Minute}}
	for _, v := range durations {
		if raw := os.Getenv(v.name); raw != "" {
			n, e := time.ParseDuration(raw)
			if e != nil || n < v.min || n > v.max {
				return c, fmt.Errorf("invalid %s", v.name)
			}
			*v.dest = n
		}
	}
	if c.Deadline <= c.Lease || c.BackoffCap < c.BackoffBase {
		return c, errors.New("invalid worker duration relationship")
	}
	return c, nil
}

type Claim struct {
	JobID, ArchiveID, DocumentID, Key, SHA, Token string
	Bytes                                         int64
	Attempts, ParserAttempts                      int
}
type Engine struct {
	DB           *sql.DB
	Storage      Storage
	Scan         Scanner
	Parse        Parser
	Config       Config
	Logger       *log.Logger
	BeforeCommit func() error
}

func Open(databaseURL string, storage Storage, scan Scanner, parse Parser, c Config) (*Engine, error) {
	if databaseURL == "" || storage == nil || scan == nil || parse == nil {
		return nil, errors.New("worker dependencies required")
	}
	db, e := sql.Open("pgx", databaseURL)
	if e != nil {
		return nil, e
	}
	var role string
	var elevated bool
	e = db.QueryRow(`SELECT current_user,rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &elevated)
	if e != nil || role != "invoyis_document_worker" || elevated {
		db.Close()
		return nil, errors.New("worker requires dedicated restricted role")
	}
	return &Engine{DB: db, Storage: storage, Scan: scan, Parse: parse, Config: c, Logger: log.Default()}, nil
}
func (e *Engine) Close() error { return e.DB.Close() }
func (e *Engine) Reap(ctx context.Context) error {
	_, err := e.DB.ExecContext(ctx, `SELECT invoyis.worker_reap($1,$2)`, e.Config.MaxClaims, e.Config.MaxParserStarts)
	return err
}
func (e *Engine) Claim(ctx context.Context) (*Claim, error) {
	var c Claim
	err := e.DB.QueryRowContext(ctx, `SELECT job_id,archive_id,document_id,storage_key,byte_count,sha256,token,claims,parser_starts FROM invoyis.worker_claim($1,$2)`, e.Config.MaxClaims, int(e.Config.Lease.Seconds())).Scan(&c.JobID, &c.ArchiveID, &c.DocumentID, &c.Key, &c.Bytes, &c.SHA, &c.Token, &c.Attempts, &c.ParserAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
func (e *Engine) boolCall(ctx context.Context, query string, args ...any) (bool, error) {
	var ok bool
	err := e.DB.QueryRowContext(ctx, query, args...).Scan(&ok)
	return ok, err
}
func (e *Engine) Heartbeat(ctx context.Context, c Claim) (bool, error) {
	return e.boolCall(ctx, `SELECT invoyis.worker_heartbeat($1,$2,$3)`, c.JobID, c.Token, int(e.Config.Lease.Seconds()))
}
func (e *Engine) backoff(attempts int) int {
	base := float64(e.Config.BackoffBase.Seconds())
	delay := math.Min(float64(e.Config.BackoffCap.Seconds()), base*math.Pow(2, float64(min(attempts-1, 16))))
	return max(1, int(delay*(0.75+rand.Float64()*0.5)))
}
func (e *Engine) outcome(ctx context.Context, c Claim, code string, retry bool) error {
	var ok bool
	var err error
	if retry && c.Attempts < e.Config.MaxClaims {
		ok, err = e.boolCall(ctx, `SELECT invoyis.worker_retry($1,$2,$3,$4,$5)`, c.JobID, c.Token, code, e.backoff(c.Attempts), e.Config.MaxClaims)
	} else {
		ok, err = e.boolCall(ctx, `SELECT invoyis.worker_fail($1,$2,$3)`, c.JobID, c.Token, code)
	}
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("lease lost before outcome")
	}
	return nil
}
func (e *Engine) Process(ctx context.Context, c Claim) error {
	attempt, cancel := context.WithTimeout(ctx, e.Config.Deadline)
	defer cancel()
	lost := make(chan struct{})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(e.Config.Lease / 3)
		defer ticker.Stop()
		defer close(lost)
		for {
			select {
			case <-done:
				return
			case <-attempt.Done():
				return
			case <-ticker.C:
				ok, err := e.Heartbeat(attempt, c)
				if err != nil || !ok {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(done); <-lost }()
	if c.Bytes < 1 || c.Bytes > e.Config.MaxBytes {
		return e.outcome(ctx, c, "content_limit_exceeded", false)
	}
	data, err := e.Storage.Read(attempt, c.Key, e.Config.MaxBytes)
	if err != nil {
		if attempt.Err() != nil {
			return attempt.Err()
		}
		return e.outcome(ctx, c, "storage_unavailable", true)
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != c.Bytes || hex.EncodeToString(digest[:]) != c.SHA {
		return e.outcome(ctx, c, "original_integrity_mismatch", false)
	}
	clean, err := e.Scan(attempt, data)
	if err != nil {
		if attempt.Err() != nil {
			return attempt.Err()
		}
		return e.outcome(ctx, c, "scanner_unavailable", true)
	}
	if !clean {
		return e.outcome(ctx, c, "scan_rejected", false)
	}
	if c.ParserAttempts >= e.Config.MaxParserStarts {
		return e.outcome(ctx, c, "parser_attempts_exhausted", false)
	}
	ok, err := e.boolCall(attempt, `SELECT invoyis.worker_start_parser($1,$2,$3)`, c.JobID, c.Token, e.Config.MaxParserStarts)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("lease lost or parser budget spent")
	}
	c.ParserAttempts++
	result, err := e.Parse(attempt, data)
	if err != nil {
		if attempt.Err() != nil {
			return attempt.Err()
		}
		var failure *ParseFailure
		if errors.As(err, &failure) {
			if failure.Retry && c.ParserAttempts >= e.Config.MaxParserStarts {
				return e.outcome(ctx, c, "parser_attempts_exhausted", false)
			}
			return e.outcome(ctx, c, failure.Code, failure.Retry)
		}
		if c.ParserAttempts >= e.Config.MaxParserStarts {
			return e.outcome(ctx, c, "parser_attempts_exhausted", false)
		}
		return e.outcome(ctx, c, "parser_resource_exhausted", true)
	}
	if e.BeforeCommit != nil {
		if err = e.BeforeCommit(); err != nil {
			return err
		}
	}
	fields, _ := json.Marshal(result.Candidates)
	sources, _ := json.Marshal(result.Provenance)
	notices, _ := json.Marshal(result.Warnings)
	ok, err = e.boolCall(attempt, `SELECT invoyis.worker_complete($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8::jsonb)`, c.JobID, c.Token, c.SHA, ExtractorVersion, result.Profile, string(fields), string(sources), string(notices))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("lease lost before completion")
	}
	return nil
}
func (e *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(e.Config.Poll)
	defer ticker.Stop()
	for {
		if err := e.Reap(ctx); err != nil && ctx.Err() == nil {
			e.Logger.Printf("worker reaping failed: %v", err)
		}
		claim, err := e.Claim(ctx)
		if err != nil && ctx.Err() == nil {
			e.Logger.Printf("worker claim failed: %v", err)
		}
		if claim != nil {
			if err := e.Process(ctx, *claim); err != nil && ctx.Err() == nil {
				e.Logger.Printf("job attempt failed: %v", err)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func ClamScan(ctx context.Context, data []byte) (bool, error) {
	binary := os.Getenv("CLAMSCAN_BINARY")
	if binary == "" {
		binary = "clamscan"
	}
	args := []string{"--no-summary"}
	if database := os.Getenv("CLAMAV_DATABASE"); database != "" {
		args = append(args, "--database="+database)
	}
	args = append(args, "-")
	command := exec.CommandContext(ctx, binary, args...)
	command.Stdin = bytes.NewReader(data)
	output, err := command.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	_ = output
	return false, err
}

type ParseFailure struct {
	Code  string
	Retry bool
}

func (p *ParseFailure) Error() string { return p.Code }

const ExtractorVersion = "factur-x-document-v1"
