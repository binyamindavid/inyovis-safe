package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/archive"
	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/auth"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if err := validateClientIPConfig(os.Getenv("BROWSER_ORIGIN"), os.Getenv("TRUSTED_CLIENT_IP_HEADER")); err != nil {
		log.Fatal(err)
	}
	sender, err := auth.SenderFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	authService, err := auth.New(os.Getenv("DATABASE_URL"), os.Getenv("AUTH_SECRET"), os.Getenv("API_GATEWAY_SECRET"), os.Getenv("BROWSER_ORIGIN"), sender)
	if err != nil {
		log.Fatal(err)
	}
	defer authService.Close()
	if err := authService.DB.PingContext(ctx); err != nil {
		log.Fatal(err)
	}
	archiveService, err := archive.New(os.Getenv("ARCHIVE_DATABASE_URL"), authService, sender, os.Getenv("BROWSER_ORIGIN"))
	if err != nil {
		log.Fatal(err)
	}
	defer archiveService.Close()
	storage, err := archive.StorageFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	archiveService.Storage = storage
	if capText := os.Getenv("UPLOAD_MAX_BYTES"); capText != "" {
		capValue, parseErr := strconv.ParseInt(capText, 10, 64)
		if parseErr != nil || capValue < 1 || capValue > 100<<20 {
			log.Fatal("invalid UPLOAD_MAX_BYTES")
		}
		archiveService.UploadCap = capValue
	}
	if discoveryURL := os.Getenv("UPLOAD_RECONCILER_DATABASE_URL"); discoveryURL != "" {
		if !strings.Contains(discoveryURL, "invoyis_upload_reconciler") {
			log.Fatal("reconciler requires its dedicated restricted role")
		}
		discovery, openErr := sql.Open("pgx", discoveryURL)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer discovery.Close()
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, reconcileErr := archiveService.ReconcileUploads(ctx, discovery); reconcileErr != nil {
						log.Printf("upload reconciliation failed: %v", reconcileErr)
					}
				}
			}
		}()
	}
	go authService.RunOutbox(ctx)
	go archiveService.RunInvitationOutbox(ctx)

	server := &http.Server{
		Addr:              addr,
		Handler:           newHandler(authService, archiveService),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("API listening on %s", addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Fatal(err)
		}
	}
}

func validateClientIPConfig(browserOrigin, trustedHeader string) error {
	origin, err := url.Parse(browserOrigin)
	if err != nil {
		return err
	}
	if trustedHeader != "" {
		return nil
	}
	host := origin.Hostname()
	if host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
		return nil
	}
	return errors.New("TRUSTED_CLIENT_IP_HEADER is required outside local development; ingress must overwrite it with one verified client IP")
}

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func newHandler(authService *auth.Service, archiveService *archive.Service) http.Handler {
	mux := http.NewServeMux()
	if authService != nil {
		authService.Register(mux)
	}
	if archiveService != nil {
		archiveService.Register(mux)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(requestID) {
			var bytes [16]byte
			if _, err := rand.Read(bytes[:]); err != nil {
				http.Error(w, "request ID unavailable", http.StatusInternalServerError)
				return
			}
			bytes[6] = (bytes[6] & 0x0f) | 0x40
			bytes[8] = (bytes[8] & 0x3f) | 0x80
			requestID = fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
		}
		log.Printf("health request_id=%s", requestID)
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"status\":\"ok\"}\n")
	})
	return mux
}
