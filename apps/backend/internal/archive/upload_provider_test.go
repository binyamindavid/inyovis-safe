package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func postGrant(t *testing.T, grant UploadGrant, keyOverride string, body []byte) int {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for key, value := range grant.Fields {
		if key == "key" && keyOverride != "" {
			value = keyOverride
		}
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", "fixture.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(body); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	req, err := http.NewRequest("POST", grant.URL, &buffer)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", providerTestOrigin())
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	if response.StatusCode >= 400 {
		t.Logf("POST response %d %s", response.StatusCode, data)
	}
	return response.StatusCode
}
func providerTestOrigin() string {
	if origin := os.Getenv("BROWSER_ORIGIN"); origin != "" {
		return origin
	}
	return "https://localhost:3000"
}
func corsAllowsToken(header, required string) bool {
	for _, token := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(token), required) {
			return true
		}
	}
	return false
}
func TestCorsAllowsToken(t *testing.T) {
	for _, tc := range []struct {
		header, required string
		want             bool
	}{
		{"GET, post, HEAD", "POST", true},
		{"x-request-id, Content-Type", "content-type", true},
		{"GET, HEAD", "POST", false},
		{"x-content-type", "content-type", false},
		{"", "content-type", false},
	} {
		if got := corsAllowsToken(tc.header, tc.required); got != tc.want {
			t.Errorf("corsAllowsToken(%q, %q) = %t, want %t", tc.header, tc.required, got, tc.want)
		}
	}
}
func TestSeaweedFSUploadPolicy(t *testing.T) {
	if os.Getenv("UPLOAD_PROVIDER_TEST") != "1" {
		t.Skip("real provider is opt-in")
	}
	storage, err := StorageFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("synthetic content <xml>invoice</xml>\n"), 145000)...)
	fixture = append(fixture, []byte("%%EOF")...)
	if len(fixture) < 4500000 {
		t.Fatal("fixture too small")
	}
	key := "staging/provider-test/" + time.Now().Format("20060102150405")
	grant, err := storage.Grant(ctx, key, time.Now().Add(15*time.Minute), 25<<20)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Fields["key"] != key {
		t.Fatalf("grant key %q", grant.Fields["key"])
	}
	status := postGrant(t, grant, "", fixture)
	if status < 200 || status >= 300 {
		t.Fatalf("valid post %d", status)
	}
	got, err := storage.Read(ctx, key, 25<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fixture) {
		t.Fatal("staged bytes changed")
	}
	status = postGrant(t, grant, key+"-other", fixture)
	if status >= 200 && status < 300 {
		t.Fatal("grant accepted another key")
	}
	status = postGrant(t, grant, "", bytes.Repeat([]byte("x"), (25<<20)+1))
	if status >= 200 && status < 300 {
		t.Fatal("grant accepted oversized object")
	}
	finalKey := "originals/provider-test/" + time.Now().Format("20060102150405")
	if err = storage.PutOriginal(ctx, finalKey, fixture); err != nil {
		t.Fatal(err)
	}
	readback, err := storage.Read(ctx, finalKey, 25<<20)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(readback) != sha256.Sum256(fixture) {
		t.Fatal("final digest mismatch")
	}
	if err = storage.PutOriginal(ctx, finalKey, []byte("different")); err == nil {
		t.Fatal("conditional final PUT overwrote existing object")
	}
	publicURL, err := url.Parse(grant.URL)
	if err != nil {
		t.Fatal(err)
	}
	publicURL.Path = "/" + storage.bucket + "/" + finalKey
	publicURL.RawQuery = ""
	unauthenticated, err := http.Get(publicURL.String())
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusForbidden {
		t.Fatalf("private original returned %d without credentials", unauthenticated.StatusCode)
	}
	preflight, _ := http.NewRequest("OPTIONS", grant.URL, nil)
	preflight.Header.Set("Origin", providerTestOrigin())
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "content-type")
	t.Logf("CORS request: Origin=%q Access-Control-Request-Method=%q Access-Control-Request-Headers=%q", preflight.Header.Get("Origin"), preflight.Header.Get("Access-Control-Request-Method"), preflight.Header.Get("Access-Control-Request-Headers"))
	resp, err := http.DefaultClient.Do(preflight)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	t.Logf("CORS response: status=%d Access-Control-Allow-Origin=%q Access-Control-Allow-Methods=%q Access-Control-Allow-Headers=%q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"), resp.Header.Get("Access-Control-Allow-Methods"), resp.Header.Get("Access-Control-Allow-Headers"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 ||
		resp.Header.Get("Access-Control-Allow-Origin") != providerTestOrigin() ||
		!corsAllowsToken(resp.Header.Get("Access-Control-Allow-Methods"), "POST") ||
		!corsAllowsToken(resp.Header.Get("Access-Control-Allow-Headers"), "content-type") {
		t.Fatalf("CORS preflight %d %v", resp.StatusCode, resp.Header)
	}
}
func TestBrowserStoredFixture(t *testing.T) {
	key := os.Getenv("BROWSER_STORAGE_KEY")
	path := os.Getenv("BROWSER_FIXTURE_PATH")
	if key == "" || path == "" {
		t.Skip("browser fixture not configured")
	}
	storage, err := StorageFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := storage.Read(context.Background(), key, 25<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, stored) {
		t.Fatal("browser original differs from fixture")
	}
	t.Logf("browser original exact bytes=%d sha256=%x", len(stored), sha256.Sum256(stored))
}

func TestSeaweedFSRecoveryBeforeFinalWrite(t *testing.T) {
	if os.Getenv("UPLOAD_PROVIDER_TEST") != "1" || os.Getenv("ARCHIVE_TEST_DATABASE_URL") == "" {
		t.Skip("provider and database required")
	}
	service, _, owner, admin := setup(t)
	storage, err := StorageFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	service.Storage = storage
	ctx := context.Background()
	archive, err := service.Create(ctx, admin, "personal", "Provider recovery", "")
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("synthetic XML <x/>\n"), 10000)...)
	session, err := service.CreateUpload(ctx, admin, archive.ID, claim(data, "provider-before-write"))
	if err != nil {
		t.Fatal(err)
	}
	status := postGrant(t, session.Grant, "", data)
	if status < 200 || status >= 300 {
		t.Fatalf("stage post: %d", status)
	}
	service.BeforeFinalWrite = func() error { return errors.New("synthetic pre-write crash") }
	if _, err = service.CompleteUpload(ctx, admin, archive.ID, session.ID, false); err == nil {
		t.Fatal("expected interruption")
	}
	var finalKey string
	if err = owner.QueryRow(`SELECT final_key FROM invoyis.upload_sessions WHERE id=$1`, session.ID).Scan(&finalKey); err != nil {
		t.Fatal(err)
	}
	if _, err = storage.Read(ctx, finalKey, 25<<20); !errors.Is(err, ErrObjectMissing) {
		t.Fatalf("missing final mapped to %v", err)
	}
	service.BeforeFinalWrite = nil
	if _, err = owner.Exec(`UPDATE invoyis.upload_sessions SET finalising_at=now()-interval '1 minute' WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	discovery, err := sql.Open("pgx", os.Getenv("UPLOAD_RECONCILER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.Close()
	done, err := service.ReconcileUploads(ctx, discovery)
	if err != nil || done != 1 {
		t.Fatalf("real storage recovery %d %v", done, err)
	}
	final, err := storage.Read(ctx, finalKey, 25<<20)
	if err != nil || !bytes.Equal(final, data) {
		t.Fatalf("real final differs: %v", err)
	}
}
