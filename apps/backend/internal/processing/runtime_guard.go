package processing

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxProductionCgroupBytes = 1 << 30

func ValidateProductionRuntime() error {
	if os.Getenv("WORKER_PRODUCTION") != "1" {
		return nil
	}
	if runtime.GOOS != "linux" {
		return errors.New("production worker requires Linux cgroup memory enforcement")
	}
	if err := checkCgroupMemoryLimit("/sys/fs/cgroup"); err != nil {
		return err
	}
	if err := checkSignatureDatabase(time.Now()); err != nil {
		return err
	}
	return nil
}

func checkCgroupMemoryLimit(root string) error {
	for _, name := range []string{"memory.max", "memory/memory.limit_in_bytes"} {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		limit, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
		if err != nil || limit < 1 || limit > maxProductionCgroupBytes {
			return errors.New("production worker requires a cgroup memory limit at or below 1 GiB")
		}
		return nil
	}
	return errors.New("production worker requires a readable cgroup memory limit")
}
