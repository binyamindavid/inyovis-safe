package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/auth"
)

type diskOriginals struct{ root string }

func (d diskOriginals) path(key string) string { return filepath.Join(d.root, filepath.FromSlash(key)) }
func (d diskOriginals) Grant(_ context.Context, key string, expiry time.Time, _ int64) (UploadGrant, error) {
	return UploadGrant{Method: "POST", URL: "https://storage.example.test/private", Fields: map[string]string{"key": key}, Headers: map[string]string{}, ExpiresAt: expiry.Format(time.RFC3339)}, nil
}
func (d diskOriginals) Read(_ context.Context, key string, cap int64) ([]byte, error) {
	file, err := os.Open(d.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrObjectMissing
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, cap+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > cap {
		return nil, ErrInvalid
	}
	return data, nil
}
func (d diskOriginals) PutOriginal(_ context.Context, key string, data []byte) error {
	path := d.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}
func TestUploadCrashChild(t *testing.T) {
	mode := os.Getenv("UPLOAD_CRASH_CHILD")
	if mode == "" {
		return
	}
	ownerURL := os.Getenv("ARCHIVE_TEST_DATABASE_URL")
	runtimeURL := os.Getenv("TEST_ARCHIVE_DATABASE_URL")
	sender := &memorySender{links: map[string]string{}, codes: map[string]string{}, bodies: map[string]string{}}
	authService, err := auth.New(ownerURL, strings.Repeat("ab", 32), "synthetic-gateway-secret-32-characters", "https://localhost:3000", sender)
	if err != nil {
		t.Fatal(err)
	}
	defer authService.Close()
	service, err := New(runtimeURL, authService, sender, "https://localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.Storage = diskOriginals{root: os.Getenv("UPLOAD_CRASH_STORAGE_DIR")}
	accountID, err := strconv.ParseInt(os.Getenv("UPLOAD_CRASH_ACCOUNT_ID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	archiveID, sessionID := os.Getenv("UPLOAD_CRASH_ARCHIVE_ID"), os.Getenv("UPLOAD_CRASH_SESSION_ID")
	switch mode {
	case "before":
		service.BeforeFinalWrite = func() error { syscall.Kill(os.Getpid(), syscall.SIGKILL); return errors.New("kill failed") }
	case "after":
		service.AfterFinalWrite = func() error { syscall.Kill(os.Getpid(), syscall.SIGKILL); return errors.New("kill failed") }
	case "recover":
		discovery, err := sql.Open("pgx", os.Getenv("UPLOAD_RECONCILER_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer discovery.Close()
		done, err := service.ReconcileUploads(context.Background(), discovery)
		if err != nil || done != 1 {
			t.Fatalf("recovery count=%d err=%v", done, err)
		}
		return
	default:
		t.Fatal("unknown crash mode")
	}
	if _, err = service.CompleteUpload(context.Background(), accountID, archiveID, sessionID, false); err != nil {
		t.Fatal(err)
	}
	t.Fatal("child did not terminate at crash point")
}
func TestUploadProcessKillAndRestart(t *testing.T) {
	if os.Getenv("ARCHIVE_TEST_DATABASE_URL") == "" {
		t.Skip("archive test database unavailable")
	}
	for _, point := range []string{"before", "after"} {
		t.Run(point, func(t *testing.T) {
			service, _, owner, admin := setup(t)
			ctx := context.Background()
			storage := diskOriginals{root: t.TempDir()}
			service.Storage = storage
			archive, err := service.Create(ctx, admin, "personal", "Process restart", "")
			if err != nil {
				t.Fatal(err)
			}
			payload := []byte("%PDF-1.7\nsynthetic process restart <xml/>")
			session, err := service.CreateUpload(ctx, admin, archive.ID, claim(payload, "process-"+point))
			if err != nil {
				t.Fatal(err)
			}
			if err = storage.PutOriginal(ctx, session.Grant.Fields["key"], payload); err != nil {
				t.Fatal(err)
			}
			baseEnv := append(os.Environ(), "UPLOAD_CRASH_STORAGE_DIR="+storage.root, "UPLOAD_CRASH_ACCOUNT_ID="+strconv.FormatInt(admin, 10), "UPLOAD_CRASH_ARCHIVE_ID="+archive.ID, "UPLOAD_CRASH_SESSION_ID="+session.ID)
			child := exec.Command(os.Args[0], "-test.run=^TestUploadCrashChild$")
			child.Env = append(baseEnv, "UPLOAD_CRASH_CHILD="+point)
			err = child.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !exit.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
				t.Fatalf("child was not killed: %v", err)
			}
			var state, finalKey, preparedKey string
			if err = owner.QueryRow(`SELECT state,final_key,prepared_key FROM invoyis.upload_sessions WHERE id=$1`, session.ID).Scan(&state, &finalKey, &preparedKey); err != nil {
				t.Fatal(err)
			}
			if state != "finalising" {
				t.Fatalf("intent missing after kill: %s", state)
			}
			prepared, err := storage.Read(ctx, preparedKey, 25<<20)
			if err != nil || !bytes.Equal(prepared, payload) {
				t.Fatalf("prepared bytes missing: %v", err)
			}
			_, finalErr := storage.Read(ctx, finalKey, 25<<20)
			if point == "before" && !errors.Is(finalErr, ErrObjectMissing) {
				t.Fatalf("final unexpectedly exists: %v", finalErr)
			}
			if point == "after" && finalErr != nil {
				t.Fatalf("final missing after write: %v", finalErr)
			}
			if _, err = owner.Exec(`UPDATE invoyis.upload_sessions SET finalising_at=now()-interval '1 minute' WHERE id=$1`, session.ID); err != nil {
				t.Fatal(err)
			}
			restart := exec.Command(os.Args[0], "-test.run=^TestUploadCrashChild$")
			restart.Env = append(baseEnv, "UPLOAD_CRASH_CHILD=recover")
			output, err := restart.CombinedOutput()
			if err != nil {
				t.Fatalf("restart failed: %v %s", err, output)
			}
			final, err := storage.Read(ctx, finalKey, 25<<20)
			if err != nil || !bytes.Equal(final, payload) {
				t.Fatalf("recovered final differs: %v", err)
			}
			var count int
			err = owner.QueryRow(`SELECT count(*) FROM invoyis.upload_receipts WHERE upload_session_id=$1`, session.ID).Scan(&count)
			if err != nil || count != 1 {
				t.Fatalf("receipt count %d: %v", count, err)
			}
			var actor sql.NullString
			err = owner.QueryRow(`SELECT system_actor FROM invoyis.archive_audit WHERE action='upload_received' AND details->>'uploadSessionId'=$1`, session.ID).Scan(&actor)
			if err != nil || actor.String != "upload_reconciler" {
				t.Fatalf("recovery actor: %v %v", actor, err)
			}
			expected := sha256.Sum256(payload)
			actual := sha256.Sum256(final)
			if expected != actual {
				t.Fatal("hash changed")
			}
		})
	}
}
