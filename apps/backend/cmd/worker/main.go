package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/archive"
	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/processing"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--synthetic-pdf-check" {
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
		result, err := processing.ParseIsolated(context.Background(), data)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--parse" {
		os.Exit(processing.ParserMain())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config, err := processing.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if err := processing.ValidateProductionRuntime(); err != nil {
		log.Fatal(err)
	}
	if os.Getenv("WORKER_PRODUCTION") == "1" {
		// A signature update can succeed even when the full scan exceeds the
		// container's memory limit. Probe scanning before claiming documents.
		scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		clean, scanErr := processing.ClamScan(scanCtx, []byte("Invoyis synthetic scanner startup check\n"))
		cancel()
		if scanErr != nil {
			log.Fatalf("production ClamAV startup scan failed: %v", scanErr)
		}
		if !clean {
			log.Fatal("production ClamAV startup scan rejected synthetic input")
		}
		// Empty input must reach the isolated parser and be rejected there.
		_, err := processing.ParseIsolated(ctx, nil)
		var failure *processing.ParseFailure
		if !errors.As(err, &failure) || failure.Code != "invalid_pdf" {
			log.Fatal("production worker requires a working isolated PDF parser")
		}
	} else {
		log.Print("WARNING: worker production memory and official-signature gates are disabled; use synthetic data only")
	}
	if os.Getenv("WORKER_S3_ACCESS_KEY_ID") == "" || os.Getenv("WORKER_S3_SECRET_ACCESS_KEY") == "" {
		log.Fatal("worker read-only S3 credentials required")
	}
	// The worker never receives the API's write credential as its storage client.
	os.Setenv("S3_ACCESS_KEY_ID", os.Getenv("WORKER_S3_ACCESS_KEY_ID"))
	os.Setenv("S3_SECRET_ACCESS_KEY", os.Getenv("WORKER_S3_SECRET_ACCESS_KEY"))
	storage, err := archive.StorageFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	engine, err := processing.Open(os.Getenv("DOCUMENT_WORKER_DATABASE_URL"), storage, processing.ClamScan, processing.ParseIsolated, config)
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()
	log.Print("document worker started")
	if err = engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
