package archive

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestStagedObjectMissingProblem(t *testing.T) {
	response := httptest.NewRecorder()
	writeFailure(response, ErrStagedObjectMissing, "synthetic-request")
	if response.Code != 409 {
		t.Fatalf("status %d", response.Code)
	}
	var body problem
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "UPLOAD_STAGED_OBJECT_MISSING" {
		t.Fatalf("code %s", body.Code)
	}
}
