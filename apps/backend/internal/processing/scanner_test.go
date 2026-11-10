package processing

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var eicarBytes = []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")

// The local signature keeps this deterministic without downloading a live database.
// clamscan still performs the scan and reports the positive detection itself.
func configureRealScanner(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("clamscan"); err != nil {
		t.Skip("real clamscan unavailable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "eicar.hdb"), []byte("44d88612fea8a8f36de82e1278abb02f:68:Eicar-Test-Signature\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAMAV_DATABASE", dir)
	return dir
}

func TestClamScanEICARAndFailures(t *testing.T) {
	dir := configureRealScanner(t)
	clean, err := ClamScan(context.Background(), eicarBytes)
	if err != nil || clean {
		t.Fatalf("EICAR detection: clean=%v err=%v", clean, err)
	}
	clean, err = ClamScan(context.Background(), []byte("clean synthetic document"))
	if err != nil || !clean {
		t.Fatalf("clean scan: clean=%v err=%v", clean, err)
	}
	t.Setenv("CLAMSCAN_BINARY", filepath.Join(dir, "missing-scanner"))
	if clean, err = ClamScan(context.Background(), eicarBytes); err == nil || clean {
		t.Fatalf("missing scanner was accepted: clean=%v err=%v", clean, err)
	}
	if runtime.GOOS != "windows" {
		stall := filepath.Join(dir, "stall")
		if err := os.WriteFile(stall, []byte("#!/bin/sh\nexec sleep 5\n"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CLAMSCAN_BINARY", stall)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		if clean, err = ClamScan(ctx, eicarBytes); err == nil || clean || time.Since(start) > 3*time.Second {
			t.Fatalf("scanner timeout: clean=%v err=%v elapsed=%s", clean, err, time.Since(start))
		}
	}
}

func TestSignatureDatabaseFailClosed(t *testing.T) {
	now := time.Now()
	cleanBinary := filepath.Join(t.TempDir(), "always-clean")
	if err := os.WriteFile(cleanBinary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAMSCAN_BINARY", cleanBinary)
	assertUnavailable := func(label string) {
		t.Helper()
		if err := checkSignatureDatabase(now); err == nil {
			t.Fatalf("%s database accepted", label)
		}
		if clean, err := ClamScan(context.Background(), []byte("synthetic")); err == nil || clean {
			t.Fatalf("%s database allowed clean result: clean=%v err=%v", label, clean, err)
		}
	}
	t.Setenv("CLAMAV_DATABASE", filepath.Join(t.TempDir(), "missing"))
	assertUnavailable("missing")
	dir := t.TempDir()
	t.Setenv("CLAMAV_DATABASE", dir)
	assertUnavailable("empty")
	hdb := filepath.Join(dir, "test.hdb")
	if err := os.WriteFile(hdb, []byte("signature"), 0600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(hdb, old, old); err != nil {
		t.Fatal(err)
	}
	assertUnavailable("stale")
	if err := os.Chtimes(hdb, now, now); err != nil {
		t.Fatal(err)
	}
	if err := checkSignatureDatabase(now); err != nil {
		t.Fatalf("fresh test database rejected: %v", err)
	}
	t.Setenv("WORKER_PRODUCTION", "1")
	if err := checkSignatureDatabase(now); err == nil {
		t.Fatal("production accepted a custom-signature-only database")
	}
	daily := filepath.Join(dir, "daily.cvd")
	writeDaily := func(build time.Time) {
		t.Helper()
		header := fmt.Sprintf("ClamAV-VDB:01 Jan 2026 00-00 -0000:1:1:1:md5:signature:builder:%d", build.Unix())
		payload := append([]byte(header), bytes.Repeat([]byte(" "), 512-len(header))...)
		if err := os.WriteFile(daily, payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeDaily(old)
	if err := checkSignatureDatabase(now); err == nil {
		t.Fatal("production accepted an old daily build with fresh file mtime")
	}
	writeDaily(now)
	if err := checkSignatureDatabase(now); err != nil {
		t.Fatalf("fresh daily header rejected: %v", err)
	}
	t.Setenv("CLAMAV_MAX_SIGNATURE_AGE", "0")
	if err := checkSignatureDatabase(now); err == nil {
		t.Fatal("invalid age threshold accepted")
	}
}
