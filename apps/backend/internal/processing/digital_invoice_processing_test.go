package processing

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDigitalInvoiceFixturesRestrictedWorker(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	for _, tc := range []struct {
		name, file string
		want       map[string]string
		absent     []string
	}{
		{"English columns", "digital_columns_en.pdf", map[string]string{"invoiceId": "INV00001234"}, []string{"currency", "subtotal", "tax", "total"}},
		{"Hebrew bidi", "digital_bidi_he.pdf", map[string]string{"invoiceId": "00001234", "subtotal": "100.00", "total": "117.00", "currency": "ILS"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			key := seedJob(t, owner, data)
			engine, err := Open(workerURL, &memoryStorage{objects: map[string][]byte{key: data}}, func(context.Context, []byte) (bool, error) { return true, nil }, parsePDF, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			claim, err := engine.Claim(context.Background())
			if err != nil || claim == nil {
				t.Fatalf("claim %v %v", claim, err)
			}
			if err = engine.Process(context.Background(), *claim); err != nil {
				t.Fatal(err)
			}
			var job, document, clearance string
			var candidatesJSON, provenanceJSON []byte
			err = owner.QueryRow(`SELECT j.state,d.state,d.clearance,x.candidates,x.provenance FROM invoyis.processing_jobs j JOIN invoyis.documents d ON d.id=j.document_id JOIN invoyis.extraction_runs x ON x.document_id=d.id WHERE j.id=$1`, claim.JobID).Scan(&job, &document, &clearance, &candidatesJSON, &provenanceJSON)
			if err != nil {
				t.Fatal(err)
			}
			if job != "succeeded" || document != "needs_review" || clearance != "cleared" {
				t.Fatalf("job=%s document=%s clearance=%s", job, document, clearance)
			}
			var candidates map[string]string
			var provenance map[string]map[string]any
			if err = json.Unmarshal(candidatesJSON, &candidates); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(provenanceJSON, &provenance); err != nil {
				t.Fatal(err)
			}
			for field, want := range tc.want {
				if candidates[field] != want {
					t.Errorf("%s = %q, want %q", field, candidates[field], want)
				}
				if provenance[field]["source"] != "digital" || provenance[field]["page"] != float64(1) {
					t.Errorf("%s provenance = %v", field, provenance[field])
				}
			}
			for _, field := range tc.absent {
				if _, ok := candidates[field]; ok {
					t.Errorf("unexpected %s = %q", field, candidates[field])
				}
			}
		})
	}
}
