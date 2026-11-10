package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthKeepsDirectLivenessAndRequestID(t *testing.T) {
	const id = "123e4567-e89b-42d3-a456-426614174000"
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", id)
	response := httptest.NewRecorder()
	newHandler(nil, nil).ServeHTTP(response, req)
	if response.Code != 200 || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unexpected liveness response: %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Request-ID") != id {
		t.Fatalf("request ID was not propagated: %q", response.Header().Get("X-Request-ID"))
	}
}

func TestProductionRequiresTrustedClientIPHeader(t *testing.T) {
	if err := validateClientIPConfig("https://app.example", ""); err == nil {
		t.Fatal("production origin accepted without trusted client IP header")
	}
	if err := validateClientIPConfig("https://app.example", "X-Verified-Client-IP"); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"https://localhost:3000", "https://127.0.0.1:3000", "https://[::1]:3000"} {
		if err := validateClientIPConfig(origin, ""); err != nil {
			t.Fatalf("local origin %s: %v", origin, err)
		}
	}
}
