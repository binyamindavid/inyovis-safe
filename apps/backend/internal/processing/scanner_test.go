package processing

import (
	"context"
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
