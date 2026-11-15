package archive

import "testing"

func TestStorageReconcilesAcrossGroupsAndPages(t *testing.T) {
	refs := []storageReference{
		{objectID: "a", documentID: "d1", bytes: 100, categoryID: "c1", categoryName: "Utilities", supplierID: "s1", supplierName: "Supplier", documentType: "invoice", format: "PDF", eligible: true},
		{objectID: "a", documentID: "d2", bytes: 100, categoryID: "c2", categoryName: "Office", supplierID: "s1", supplierName: "Supplier", documentType: "bill", format: "PDF"},
		{objectID: "b", documentID: "d3", bytes: 250, categoryID: "c1", categoryName: "Utilities", supplierID: "s2", supplierName: "Another supplier", documentType: "invoice", format: "PDF"},
		{objectID: "c", documentID: "d4", bytes: 50},
	}
	for _, by := range []string{"category", "company", "format", "type"} {
		t.Run(by, func(t *testing.T) {
			r := aggregateStorage(refs, by, "overview")
			if r.Bytes != 400 || r.OriginalCount != 3 || r.DocumentCount != 4 {
				t.Fatalf("headline: %+v", r)
			}
			var sum int64
			seen := map[string]bool{}
			for _, g := range r.Groups {
				sum += g.Bytes
				for _, d := range g.Documents {
					if seen[d.ID] {
						t.Fatalf("duplicate drill-down document %s", d.ID)
					}
					seen[d.ID] = true
				}
			}
			if sum != r.Bytes || len(seen) != 4 {
				t.Fatalf("breakdown: %+v", r)
			}
			if by == "category" || by == "type" {
				found := false
				for _, g := range r.Groups {
					if g.ID == "shared" {
						found = g.Bytes == 100 && g.DocumentCount == 2
					}
				}
				if !found {
					t.Fatalf("missing shared original: %+v", r)
				}
			}
		})
	}
	for _, by := range []string{"category", "company", "format", "type"} {
		r := aggregateStorage(refs, by, "review")
		if r.Bytes != 100 || r.OriginalCount != 1 || r.DocumentCount != 1 || len(r.Groups) != 1 || !r.Groups[0].Documents[0].SharedWithRetained {
			t.Fatalf("review %s: %+v", by, r)
		}
	}
	if empty := aggregateStorage(nil, "category", "overview"); empty.Bytes != 0 || empty.Groups == nil {
		t.Fatalf("empty: %+v", empty)
	}
}
