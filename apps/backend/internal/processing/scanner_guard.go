package processing

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var errSignatureDatabase = errors.New("scanner signature database unavailable")

func checkSignatureDatabase(now time.Time) error {
	directory := os.Getenv("CLAMAV_DATABASE")
	if directory == "" {
		return errSignatureDatabase
	}
	maxAge := 48 * time.Hour
	if configured := os.Getenv("CLAMAV_MAX_SIGNATURE_AGE"); configured != "" {
		parsed, err := time.ParseDuration(configured)
		if err != nil || parsed <= 0 || parsed > 7*24*time.Hour {
			return errSignatureDatabase
		}
		maxAge = parsed
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return errSignatureDatabase
	}
	var fresh bool
	var officialDaily bool
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		extension := filepath.Ext(name)
		switch extension {
		case ".cvd", ".cld", ".hdb", ".hsb", ".ndb", ".ldb", ".mdb":
		default:
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.ModTime().After(now.Add(5*time.Minute)) || now.Sub(info.ModTime()) > maxAge {
			continue
		}
		fresh = true
		if name == "daily.cvd" || name == "daily.cld" {
			if officialDatabaseFresh(filepath.Join(directory, entry.Name()), now, maxAge) {
				officialDaily = true
			}
		}
	}
	if !fresh || (os.Getenv("WORKER_PRODUCTION") == "1" && !officialDaily) {
		return errSignatureDatabase
	}
	return nil
}

// CVD and CLD headers include the database build epoch in their final field.
// clamscan remains responsible for validating and loading the database itself.
func officialDatabaseFresh(path string, now time.Time, maxAge time.Duration) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 512)
	count, err := file.Read(header)
	if err != nil || count < 100 {
		return false
	}
	fields := strings.Split(strings.TrimSpace(strings.TrimRight(string(header), "\x00")), ":")
	if len(fields) < 9 || fields[0] != "ClamAV-VDB" {
		return false
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(fields[len(fields)-1]), 10, 64)
	if err != nil || epoch <= 0 {
		return false
	}
	built := time.Unix(epoch, 0)
	return !built.After(now.Add(5*time.Minute)) && now.Sub(built) <= maxAge
}
