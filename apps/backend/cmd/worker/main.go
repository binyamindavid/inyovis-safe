package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/archive"
	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/processing"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--parse" {
		os.Exit(processing.ParserMain())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config, err := processing.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
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
