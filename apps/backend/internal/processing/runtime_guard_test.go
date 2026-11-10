package processing

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionCgroupMemoryGate(t *testing.T) {
	dir := t.TempDir()
	if err := checkCgroupMemoryLimit(dir); err == nil {
		t.Fatal("missing memory cap accepted")
	}
	path := filepath.Join(dir, "memory.max")
	for _, tc := range []struct {
		value string
		allow bool
	}{{"max", false}, {"2147483648", false}, {"1073741824", true}, {"536870912", true}} {
		if err := os.WriteFile(path, []byte(tc.value), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkCgroupMemoryLimit(dir); (err == nil) != tc.allow {
			t.Fatalf("memory.max=%s allow=%v err=%v", tc.value, tc.allow, err)
		}
	}
}

func TestProductionRejectsScannerOverride(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux production guard")
	}
	t.Setenv("WORKER_PRODUCTION", "1")
	t.Setenv("CLAMSCAN_BINARY", "/tmp/always-clean")
	if err := ValidateProductionRuntime(); err == nil || !strings.Contains(err.Error(), "override") {
		t.Fatalf("scanner override accepted: %v", err)
	}
}
