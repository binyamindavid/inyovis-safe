package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           newHandler(),
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

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func newHandler() http.Handler {
	mux := http.NewServeMux()
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
