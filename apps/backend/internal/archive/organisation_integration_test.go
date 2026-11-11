package archive

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOrganisationHTTPStatuses(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	a, e := s.Create(ctx, actor, "personal", "HTTP", "")
	if e != nil {
		t.Fatal(e)
	}
	token := "synthetic-organisation-session"
	digest := sha256.Sum256([]byte(token))
	if _, e = owner.Exec(`INSERT INTO invoyis.sessions(account_id,token_digest,expires_at) VALUES($1,$2,$3)`, actor, digest[:], time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	request := func(name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/archives/"+a.ID+"/organisation/suppliers", strings.NewReader(fmt.Sprintf(`{"displayName":%q}`, name)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Invoyis-Gateway-Key", s.Auth.GatewaySecret)
		req.Header.Set("X-Invoyis-Client-IP", "127.0.0.1")
		req.Header.Set("X-Invoyis-Intent", "browser")
		req.Header.Set("Origin", s.BrowserOrigin)
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	first := request("Café")
	if first.Code != 201 {
		t.Fatalf("first %d %s", first.Code, first.Body.String())
	}
	replay := request("CAFÉ")
	if replay.Code != 200 || !strings.Contains(replay.Body.String(), `"displayName":"Café"`) {
		t.Fatalf("replay %d %s", replay.Code, replay.Body.String())
	}
	var id string
	if e = owner.QueryRow(`SELECT id FROM invoyis.suppliers WHERE archive_id=$1`, a.ID).Scan(&id); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeOrganisation(ctx, actor, a.ID, "suppliers", id, "archive", ""); e != nil {
		t.Fatal(e)
	}
	conflict := request("café")
	if conflict.Code != 409 || !strings.Contains(conflict.Body.String(), `"recordId":"`+id+`"`) || !strings.Contains(conflict.Body.String(), `"archived":true`) {
		t.Fatalf("conflict %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestOrganisationKeysV1(t *testing.T) {
	cases := []struct{ input, key, broad string }{
		{"  Café   Straße  ", "café strasse", "cafe strasse"},
		{"Cafe\u0301", "café", "cafe"},
		{"מֶלֶךְ", "מֶלֶךְ", "מלך"},
		{"מלכ", "מלכ", "מלכ"},
		{"क़", "क़", "क़"},
	}
	for _, c := range cases {
		_, k, b, e := organisationKeys(c.input)
		if e != nil || k != c.key || b != c.broad {
			t.Errorf("%q: %q %q %v", c.input, k, b, e)
		}
	}
	if _, _, _, e := organisationKeys("  "); !errors.Is(e, ErrInvalid) {
		t.Errorf("empty: %v", e)
	}
	if _, _, _, e := organisationKeys("A\u200fB"); !errors.Is(e, ErrInvalid) {
		t.Errorf("bidi formatting: %v", e)
	}
}

func TestSupplierSuggestionsAndArchiveVisibility(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	a, e := s.Create(ctx, actor, "personal", "Suggestions", "")
	if e != nil {
		t.Fatal(e)
	}
	accented, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Café")
	if e != nil {
		t.Fatal(e)
	}
	plain, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Cafe")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmSupplierAlias(ctx, actor, a.ID, accented.ID, "CAFÉ  "); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmSupplierAlias(ctx, actor, a.ID, accented.ID, "Coffee"); e != nil {
		t.Fatal(e)
	}
	items, e := s.SuggestSuppliers(ctx, actor, a.ID, "cafe")
	if e != nil || len(items) != 2 {
		t.Fatalf("suggestions: %v %#v", e, items)
	}
	if items[0].ID != plain.ID || items[0].Match != "exact" || items[1].ID != accented.ID || items[1].Match != "broad" {
		t.Fatalf("match ranking: %#v", items)
	}
	if _, e = s.ChangeOrganisation(ctx, actor, a.ID, "suppliers", plain.ID, "archive", ""); e != nil {
		t.Fatal(e)
	}
	items, e = s.SuggestSuppliers(ctx, actor, a.ID, "cafe")
	if e != nil || len(items) != 1 || items[0].ID != accented.ID {
		t.Fatalf("archived suggestions: %#v %v", items, e)
	}
	listed, e := s.ListOrganisation(ctx, actor, a.ID, "suppliers", false)
	if e != nil || len(listed) != 1 {
		t.Fatalf("active listing: %#v %v", listed, e)
	}
	listed, e = s.ListOrganisation(ctx, actor, a.ID, "suppliers", true)
	if e != nil || len(listed) != 2 {
		t.Fatalf("admin listing: %#v %v", listed, e)
	}
	viewer := addAccount(t, owner, "viewer-suggest@example.test")
	contributor := addAccount(t, owner, "contributor-suggest@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'contributor')`, a.ID, contributor); e != nil {
		t.Fatal(e)
	}
	if got, e := s.ListOrganisation(ctx, contributor, a.ID, "suppliers", false); e != nil || len(got) != 1 {
		t.Fatalf("contributor read: %v %#v", e, got)
	}
	if changed, e := s.ConfirmSupplierAlias(ctx, contributor, a.ID, accented.ID, "Contributor alias"); e != nil || !changed {
		t.Fatalf("contributor confirmation: %v %v", changed, e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, viewer); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ListOrganisation(ctx, viewer, a.ID, "suppliers", true); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer archived listing: %v", e)
	}
	if rec, e := s.GetOrganisation(ctx, viewer, a.ID, "suppliers", plain.ID); e != nil || !rec.Archived {
		t.Fatalf("historical lookup: %#v %v", rec, e)
	}
	if e = s.ReassignSupplierAlias(ctx, actor, a.ID, "Coffee", accented.ID, plain.ID); !errors.Is(e, ErrConflict) {
		t.Fatalf("archived target: %v", e)
	}
}

func TestOrganisationConcurrentKindsAndIsolation(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	a, e := s.Create(ctx, actor, "personal", "One", "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(ctx, actor, "personal", "Two", "")
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"categories", "properties", "paper-locations"} {
		var wg sync.WaitGroup
		ids := make([]string, 5)
		errs := make([]error, 5)
		for i := range ids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				var rec OrganisationRecord
				rec, _, errs[i] = s.CreateOrganisation(ctx, actor, a.ID, kind, "Same  Name")
				ids[i] = rec.ID
			}(i)
		}
		wg.Wait()
		for i := range ids {
			if errs[i] != nil || ids[i] != ids[0] {
				t.Fatalf("%s: %v %v", kind, ids, errs)
			}
		}
		rec, created, e := s.CreateOrganisation(ctx, actor, b.ID, kind, "same name")
		if e != nil || !created || rec.ID == ids[0] {
			t.Fatalf("archive isolation %s: %v", kind, e)
		}
	}
	first, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "מלך")
	if e != nil {
		t.Fatal(e)
	}
	second, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "מלכ")
	if e != nil || first.ID == second.ID {
		t.Fatalf("final letters: %v", e)
	}
	viewer := addAccount(t, owner, "reader@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, viewer); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetOrganisation(ctx, viewer, b.ID, "suppliers", first.ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("cross archive access: %v", e)
	}
	if _, _, e = s.CreateOrganisation(ctx, viewer, a.ID, "suppliers", "New"); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer write: %v", e)
	}
}

func TestOrganisationDirectSQLAndRaces(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	a, e := s.Create(ctx, actor, "personal", "SQL", "")
	if e != nil {
		t.Fatal(e)
	}
	one, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "First")
	if e != nil {
		t.Fatal(e)
	}
	two, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Second")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.supplier_identity_keys(archive_id,identity_key,supplier_id,kind,key_version,suggestion_key,suggestion_version) VALUES($1,'first',$2,'alias',1,'first',1)`, a.ID, two.ID); e == nil {
		t.Fatal("direct SQL canonical alias collision accepted")
	}
	if _, e = owner.Exec(`UPDATE invoyis.supplier_identity_keys SET supplier_id=$3 WHERE archive_id=$1 AND identity_key='first'`, a.ID, one.ID, two.ID); e == nil {
		t.Fatal("direct SQL registry mismatch accepted")
	}
	if _, e = owner.Exec(`DELETE FROM invoyis.supplier_identity_keys WHERE archive_id=$1 AND identity_key='first'`, a.ID); e == nil {
		t.Fatal("direct SQL canonical deletion accepted")
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.supplier_identity_keys(archive_id,identity_key,supplier_id,kind,key_version,suggestion_key,suggestion_version) VALUES($1,'alias',$2,'alias',1,'alias',1)`, a.ID, two.ID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.ChangeOrganisation(ctx, actor, a.ID, "suppliers", []string{one.ID, two.ID}[i], "rename", "Shared")
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, e := range errs {
		if e == nil {
			successes++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatalf("rename race: %v", e)
		}
	}
	if successes != 1 {
		t.Fatalf("rename winners %d: %v", successes, errs)
	}
	oldName := "First"
	if errs[1] == nil {
		oldName = "Second"
	}
	if _, created, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", oldName); e != nil || !created {
		t.Fatalf("old name became alias: %v %v", created, e)
	}
	if e = s.ReassignSupplierAlias(ctx, actor, a.ID, "alias", two.ID, one.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.ReassignSupplierAlias(ctx, actor, a.ID, "alias", two.ID, one.ID); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale reassignment: %v", e)
	}
	third, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Third")
	if e != nil {
		t.Fatal(e)
	}
	var race sync.WaitGroup
	results := make([]error, 2)
	for i, target := range []string{two.ID, third.ID} {
		race.Add(1)
		go func(i int, target string) {
			defer race.Done()
			results[i] = s.ReassignSupplierAlias(ctx, actor, a.ID, "alias", one.ID, target)
		}(i, target)
	}
	race.Wait()
	winners := 0
	for _, result := range results {
		if result == nil {
			winners++
		} else if !errors.Is(result, ErrConflict) {
			t.Fatalf("reassignment race: %v", result)
		}
	}
	if winners != 1 {
		t.Fatalf("reassignment winners %d: %v", winners, results)
	}
	// Direct restricted-role SQL cannot point a row at a different archive.
	tx, e := s.transaction(ctx, actor, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	other, e := s.Create(ctx, actor, "personal", "Other", "")
	if e != nil {
		t.Fatal(e)
	}
	id, e := newID()
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.supplier_identity_keys(archive_id,identity_key,supplier_id,kind,key_version,suggestion_key,suggestion_version) VALUES($1,$2,$3,'alias',1,$2,1)`, a.ID, fmt.Sprintf("cross-%s", strconv.FormatInt(actor, 10)), one.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.categories(id,archive_id,display_name,identity_key,suggestion_key,key_version,suggestion_version) VALUES($1,$2,'Wrong','wrong','wrong',1,1)`, id, other.ID)
	if e == nil {
		t.Fatal("cross archive restricted write accepted")
	}
}

func TestOrganisationDatabaseLifecycle(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	a, e := s.Create(ctx, actor, "personal", "Filing", "")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ids := make([]string, 8)
	errs := make([]error, 8)
	created := make([]bool, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var r OrganisationRecord
			r, created[i], errs[i] = s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "  Café   Straße  ")
			ids[i] = r.ID
		}(i)
	}
	wg.Wait()
	n := 0
	for i := range ids {
		if errs[i] != nil {
			t.Fatalf("create %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("different ids: %v", ids)
		}
		if created[i] {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("created %d", n)
	}
	if rec, e := s.ChangeOrganisation(ctx, actor, a.ID, "suppliers", ids[0], "rename", "CAFÉ   STRASSE"); e != nil || rec.ID != ids[0] || rec.DisplayName != "CAFÉ   STRASSE" {
		t.Fatalf("case/display rename: %#v %v", rec, e)
	}
	if rec, created, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "café strasse"); e != nil || created || rec.DisplayName != "CAFÉ   STRASSE" {
		t.Fatalf("stored display replay: %#v %v %v", rec, created, e)
	}
	r, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Second")
	if e != nil {
		t.Fatal(e)
	}
	if changed, e := s.ConfirmSupplierAlias(ctx, actor, a.ID, r.ID, "Alias"); e != nil || !changed {
		t.Fatalf("alias %v %v", changed, e)
	}
	if changed, e := s.ConfirmSupplierAlias(ctx, actor, a.ID, r.ID, "alias"); e != nil || changed {
		t.Fatalf("alias replay %v %v", changed, e)
	}
	if _, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "ALIAS"); !errors.Is(e, ErrConflict) {
		t.Fatalf("alias collision: %v", e)
	}
	if e = s.ReassignSupplierAlias(ctx, actor, a.ID, "Alias", r.ID, ids[0]); e != nil {
		t.Fatal(e)
	}
	if e = s.ReassignSupplierAlias(ctx, actor, a.ID, "Alias", r.ID, ids[0]); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale: %v", e)
	}
	var aliasAudits int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_audit WHERE archive_id=$1 AND action IN ('supplier_alias_confirmed','supplier_alias_reassigned')`, a.ID).Scan(&aliasAudits); e != nil || aliasAudits != 2 {
		t.Fatalf("alias audit %d %v", aliasAudits, e)
	}
	if _, e = s.ChangeOrganisation(ctx, actor, a.ID, "suppliers", ids[0], "archive", ""); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Café Straße"); !errors.Is(e, ErrConflict) {
		t.Fatalf("archived: %v", e)
	}
	if _, e = s.ChangeOrganisation(ctx, actor, a.ID, "suppliers", ids[0], "restore", ""); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_audit WHERE archive_id=$1 AND action LIKE 'organisation_suppliers_%'`, a.ID).Scan(&count); e != nil || count != 5 {
		t.Fatalf("audit %d %v", count, e)
	}
}
