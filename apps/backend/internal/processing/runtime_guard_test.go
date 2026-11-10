package processing

import (
	"os"
	"path/filepath"
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
