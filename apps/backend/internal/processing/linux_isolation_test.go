package processing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxParserIsolation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux bubblewrap check")
	}
	if os.Getenv("PROCESSING_LINUX_ISOLATION_TEST") != "1" {
		t.Skip("Linux bubblewrap integration check is opt-in")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Fatal("bwrap is required for the Linux isolation gate")
	}
	parentNet, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "private-secret")
	if err := os.WriteFile(sentinel, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "probe")
	script := "#!/usr/bin/env sh\n" +
		"[ -z \"${DOCUMENT_WORKER_DATABASE_URL:-}\" ] || exit 41\n" +
		"[ -z \"${WORKER_S3_SECRET_ACCESS_KEY:-}\" ] || exit 42\n" +
		"[ ! -e '" + sentinel + "' ] || exit 43\n" +
		"printf '{\"profile\":\"%s\",\"candidates\":{},\"provenance\":{},\"warnings\":[]}' \"$(readlink /proc/self/ns/net)\"\n"
	if err := os.WriteFile(probe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROCESSING_PARSER_BINARY", probe)
	t.Setenv("DOCUMENT_WORKER_DATABASE_URL", "must-not-reach-parser")
	t.Setenv("WORKER_S3_SECRET_ACCESS_KEY", "must-not-reach-parser")
	result, err := ParseIsolated(context.Background(), []byte("%PDF-synthetic"))
	if err != nil {
		t.Fatalf("sandboxed probe failed: %v", err)
	}
	if result.Profile == "" || result.Profile == parentNet || !strings.HasPrefix(result.Profile, "net:[") {
		t.Fatalf("network namespace not isolated: child=%q parent=%q", result.Profile, parentNet)
	}
	t.Setenv("PATH", dir)
	_, err = ParseIsolated(context.Background(), []byte("%PDF-synthetic"))
	var failure *ParseFailure
	if !errors.As(err, &failure) || failure.Code != "parser_resource_exhausted" || !failure.Retry {
		t.Fatalf("missing bwrap did not fail closed: %v", err)
	}
}
