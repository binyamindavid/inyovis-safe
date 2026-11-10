package auth

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"regexp"
	"time"
)

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type problem struct {
	Type        string       `json:"type"`
	Title       string       `json:"title"`
	Status      int          `json:"status"`
	Code        string       `json:"code"`
	RequestID   string       `json:"requestId"`
	FieldErrors []fieldError `json:"fieldErrors,omitempty"`
}
type fieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

func writeProblem(w http.ResponseWriter, status int, code, title, id string, fields ...fieldError) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", id)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: title, Status: status, Code: code, RequestID: id, FieldErrors: fields})
}

func writeJSON(w http.ResponseWriter, status int, id string, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", id)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("JSON content type required")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("single JSON object required")
	}
	return nil
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/code/request", s.guard(true, s.requestCode))
	mux.HandleFunc("POST /v1/auth/code/verify", s.guard(true, s.verifyCode))
	mux.HandleFunc("GET /v1/auth/session", s.guard(false, s.getSession))
	mux.HandleFunc("POST /v1/auth/logout", s.guard(true, s.logout))
}

func (s *Service) guard(mutation bool, next func(http.ResponseWriter, *http.Request, string, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(id) {
			id, _ = randomID()
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", id)
		log.Printf("auth request_id=%s method=%s path=%s", id, r.Method, r.URL.Path)
		secret := r.Header.Get("X-Invoyis-Gateway-Key")
		if subtle.ConstantTimeCompare([]byte(secret), []byte(s.GatewaySecret)) != 1 {
			writeProblem(w, 403, "FORBIDDEN", "Forbidden", id)
			return
		}
		ip := r.Header.Get("X-Invoyis-Client-IP")
		if net.ParseIP(ip) == nil {
			writeProblem(w, 403, "FORBIDDEN", "Forbidden", id)
			return
		}
		if mutation && (r.Header.Get("Origin") != s.BrowserOrigin || r.Header.Get("X-Invoyis-Intent") != "browser") {
			writeProblem(w, 403, "FORBIDDEN", "Forbidden", id)
			return
		}
		next(w, r, id, ip)
	}
}

func (s *Service) requestCode(w http.ResponseWriter, r *http.Request, id, ip string) {
	started := time.Now()
	defer func() {
		if remaining := 250*time.Millisecond - time.Since(started); remaining > 0 {
			time.Sleep(remaining)
		}
	}()
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeProblem(w, 422, "VALIDATION_ERROR", "Invalid request", id)
		return
	}
	if _, err := NormalizeEmail(body.Email); err != nil {
		writeProblem(w, 422, "VALIDATION_ERROR", "Invalid email", id, fieldError{"email", "INVALID_EMAIL"})
		return
	}
	limited, err := s.RequestCode(r.Context(), body.Email, ip, r.Header.Get("Accept-Language"))
	if err != nil {
		writeProblem(w, 500, "INTERNAL_ERROR", "Internal Server Error", id)
		return
	}
	if limited {
		w.Header().Set("Retry-After", "900")
		writeProblem(w, 429, "RATE_LIMITED", "Too Many Requests", id)
		return
	}
	writeJSON(w, 202, id, map[string]string{"status": "accepted"})
}

func (s *Service) verifyCode(w http.ResponseWriter, r *http.Request, id, ip string) {
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeProblem(w, 422, "VALIDATION_ERROR", "Invalid request", id)
		return
	}
	if _, err := NormalizeEmail(body.Email); err != nil {
		writeProblem(w, 422, "VALIDATION_ERROR", "Invalid email", id, fieldError{"email", "INVALID_EMAIL"})
		return
	}
	verified, limited, err := s.VerifyCode(r.Context(), body.Email, body.Code, ip)
	if limited {
		w.Header().Set("Retry-After", "900")
		writeProblem(w, 429, "RATE_LIMITED", "Too Many Requests", id)
		return
	}
	if errors.Is(err, ErrExpiredCode) {
		writeProblem(w, 422, "EXPIRED_CODE", "Expired code", id)
		return
	}
	if errors.Is(err, ErrInvalidCode) {
		writeProblem(w, 422, "INVALID_CODE", "Invalid code", id)
		return
	}
	if err != nil {
		writeProblem(w, 500, "INTERNAL_ERROR", "Internal Server Error", id)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: verified.Token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: verified.ExpiresAt})
	writeJSON(w, 200, id, map[string]string{"email": verified.Email, "expiresAt": verified.ExpiresAt.UTC().Format(time.RFC3339)})
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Service) getSession(w http.ResponseWriter, r *http.Request, id, _ string) {
	email, expiry, err := s.Session(r.Context(), sessionToken(r))
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, 401, "UNAUTHORIZED", "Unauthorized", id)
		return
	}
	if err != nil {
		writeProblem(w, 500, "INTERNAL_ERROR", "Internal Server Error", id)
		return
	}
	writeJSON(w, 200, id, map[string]string{"email": email, "expiresAt": expiry.UTC().Format(time.RFC3339)})
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request, id, _ string) {
	if err := s.Revoke(r.Context(), sessionToken(r)); err != nil {
		writeProblem(w, 500, "INTERNAL_ERROR", "Internal Server Error", id)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", id)
	w.WriteHeader(http.StatusNoContent)
}
