package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const organisationKeyVersion = 1

// organisationKeys implements the issue's v1 algorithm. Display retains internal spaces.
func organisationKeys(raw string) (display, identity, broad string, err error) {
	display, err = cleanBounded(raw, 240, false)
	if err != nil {
		return
	}
	for _, r := range display {
		if r == 0x061c || r == 0x200e || r == 0x200f {
			err = ErrInvalid
			return
		}
	}
	if display == "" {
		err = ErrInvalid
		return
	}
	identity = norm.NFC.String(cases.Fold().String(strings.Join(strings.Fields(norm.NFC.String(display)), " ")))
	if identity == "" {
		err = ErrInvalid
		return
	}
	var out []rune
	latin := false
	for _, r := range norm.NFD.String(identity) {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r) {
			hebrewPoint := r >= 0x0591 && r <= 0x05af || r >= 0x05b0 && r <= 0x05bd || r == 0x05bf || r >= 0x05c1 && r <= 0x05c2 || r >= 0x05c4 && r <= 0x05c5 || r == 0x05c7
			if hebrewPoint || latin {
				continue
			}
		} else {
			latin = unicode.In(r, unicode.Latin)
		}
		out = append(out, r)
	}
	broad = norm.NFC.String(string(out))
	return
}

type OrganisationRecord struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Archived    bool   `json:"archived"`
}
type SupplierSuggestion struct {
	OrganisationRecord
	Match string `json:"match"`
}

func (s *Service) SuggestSuppliers(ctx context.Context, actor int64, archiveID, name string) ([]SupplierSuggestion, error) {
	_, key, broad, err := organisationKeys(name)
	if err != nil {
		return nil, err
	}
	tx, err := s.orgTx(ctx, actor, archiveID, "view")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.display_name, bool_or(k.identity_key=$2) FROM invoyis.supplier_identity_keys k JOIN invoyis.suppliers s ON s.archive_id=k.archive_id AND s.id=k.supplier_id WHERE k.archive_id=$1 AND s.archived_at IS NULL AND (k.identity_key=$2 OR k.suggestion_key=$3) GROUP BY s.id,s.display_name ORDER BY bool_or(k.identity_key=$2) DESC,s.display_name,s.id`, archiveID, key, broad)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SupplierSuggestion{}
	for rows.Next() {
		var v SupplierSuggestion
		var exact bool
		if err = rows.Scan(&v.ID, &v.DisplayName, &exact); err != nil {
			return nil, err
		}
		if exact {
			v.Match = "exact"
		} else {
			v.Match = "broad"
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) ConfirmSupplierAlias(ctx context.Context, actor int64, archiveID, supplierID, name string) (bool, error) {
	if !uuidPattern.MatchString(supplierID) {
		return false, ErrInvalid
	}
	_, key, broad, err := organisationKeys(name)
	if err != nil {
		return false, err
	}
	tx, err := s.orgTx(ctx, actor, archiveID, "submit")
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var archived bool
	err = tx.QueryRowContext(ctx, `SELECT archived_at IS NOT NULL FROM invoyis.suppliers WHERE archive_id=$1 AND id=$2`, archiveID, supplierID).Scan(&archived)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if archived {
		return false, ErrConflict
	}
	var existingID, kind string
	err = tx.QueryRowContext(ctx, `SELECT supplier_id,kind FROM invoyis.supplier_identity_keys WHERE archive_id=$1 AND identity_key=$2`, archiveID, key).Scan(&existingID, &kind)
	if err == nil {
		if existingID == supplierID {
			return false, nil
		}
		var rec OrganisationRecord
		if e := tx.QueryRowContext(ctx, `SELECT id,display_name,archived_at IS NOT NULL FROM invoyis.suppliers WHERE archive_id=$1 AND id=$2`, archiveID, existingID).Scan(&rec.ID, &rec.DisplayName, &rec.Archived); e != nil {
			return false, e
		}
		return false, &identityConflict{rec}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO invoyis.supplier_identity_keys(archive_id,identity_key,supplier_id,kind,key_version,suggestion_key,suggestion_version) VALUES($1,$2,$3,'alias',1,$4,1)`, archiveID, key, supplierID, broad)
	if orgUnique(err) {
		tx.Rollback()
		rec, e := s.orgLookupKey(ctx, actor, archiveID, "suppliers", key)
		if e != nil {
			return false, e
		}
		if rec.ID == supplierID && !rec.Archived {
			return false, nil
		}
		return false, &identityConflict{rec}
	}
	if err != nil {
		return false, err
	}
	if err = s.audit(ctx, tx, archiveID, actor, "supplier_alias_confirmed", nil, map[string]any{"supplierId": supplierID, "identityKey": key}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Service) ReassignSupplierAlias(ctx context.Context, actor int64, archiveID, name, expectedID, targetID string) error {
	if !uuidPattern.MatchString(expectedID) || !uuidPattern.MatchString(targetID) {
		return ErrInvalid
	}
	_, key, _, err := organisationKeys(name)
	if err != nil {
		return err
	}
	tx, err := s.orgTx(ctx, actor, archiveID, "settings")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current, kind string
	err = tx.QueryRowContext(ctx, `SELECT supplier_id,kind FROM invoyis.supplier_identity_keys WHERE archive_id=$1 AND identity_key=$2 FOR UPDATE`, archiveID, key).Scan(&current, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if kind != "alias" || current != expectedID {
		return ErrConflict
	}
	if current == targetID {
		return nil
	}
	var archived bool
	err = tx.QueryRowContext(ctx, `SELECT archived_at IS NOT NULL FROM invoyis.suppliers WHERE archive_id=$1 AND id=$2 FOR UPDATE`, archiveID, targetID).Scan(&archived)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if archived {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE invoyis.supplier_identity_keys SET supplier_id=$3 WHERE archive_id=$1 AND identity_key=$2 AND supplier_id=$4 AND kind='alias'`, archiveID, key, targetID, expectedID); err != nil {
		return err
	}
	if err = s.audit(ctx, tx, archiveID, actor, "supplier_alias_reassigned", nil, map[string]any{"identityKey": key, "fromSupplierId": expectedID, "toSupplierId": targetID}); err != nil {
		return err
	}
	return tx.Commit()
}

type identityConflict struct{ Record OrganisationRecord }

func (e *identityConflict) Error() string { return "organisation identity reserved" }
func (e *identityConflict) Unwrap() error { return ErrConflict }

func orgTable(kind string) string {
	switch kind {
	case "suppliers":
		return "suppliers"
	case "categories":
		return "categories"
	case "properties":
		return "properties"
	case "paper-locations":
		return "paper_locations"
	}
	return ""
}
func orgUnique(err error) bool { var p *pgconn.PgError; return errors.As(err, &p) && p.Code == "23505" }

func (s *Service) orgTx(ctx context.Context, actor int64, archiveID, action string) (*sql.Tx, error) {
	if !uuidPattern.MatchString(archiveID) {
		return nil, ErrInvalid
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return nil, err
	}
	if _, _, err = s.membership(ctx, tx, archiveID, actor, action); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func (s *Service) orgLookupKey(ctx context.Context, actor int64, archiveID, kind, key string) (OrganisationRecord, error) {
	tx, err := s.orgTx(ctx, actor, archiveID, "view")
	if err != nil {
		return OrganisationRecord{}, err
	}
	defer tx.Rollback()
	table := orgTable(kind)
	var row *sql.Row
	if kind == "suppliers" {
		row = tx.QueryRowContext(ctx, `SELECT s.id,s.display_name,s.archived_at IS NOT NULL FROM invoyis.suppliers s JOIN invoyis.supplier_identity_keys k ON k.archive_id=s.archive_id AND k.supplier_id=s.id WHERE k.archive_id=$1 AND k.identity_key=$2`, archiveID, key)
	} else {
		row = tx.QueryRowContext(ctx, `SELECT id,display_name,archived_at IS NOT NULL FROM invoyis.`+table+` WHERE archive_id=$1 AND identity_key=$2`, archiveID, key)
	}
	var rec OrganisationRecord
	err = row.Scan(&rec.ID, &rec.DisplayName, &rec.Archived)
	return rec, err
}
func (s *Service) CreateOrganisation(ctx context.Context, actor int64, archiveID, kind, name string) (OrganisationRecord, bool, error) {
	table := orgTable(kind)
	if table == "" {
		return OrganisationRecord{}, false, ErrInvalid
	}
	display, key, broad, err := organisationKeys(name)
	if err != nil {
		return OrganisationRecord{}, false, err
	}
	action := "settings"
	if kind != "suppliers" {
		action = "submit"
	}
	tx, err := s.orgTx(ctx, actor, archiveID, action)
	if err != nil {
		return OrganisationRecord{}, false, err
	}
	defer tx.Rollback()
	id, err := newID()
	if err != nil {
		return OrganisationRecord{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO invoyis.`+table+`(id,archive_id,display_name,identity_key,suggestion_key,key_version,suggestion_version) VALUES($1,$2,$3,$4,$5,1,1)`, id, archiveID, display, key, broad)
	if err != nil {
		tx.Rollback()
		if orgUnique(err) {
			rec, e := s.orgLookupKey(ctx, actor, archiveID, kind, key)
			if e == nil {
				if rec.Archived {
					return OrganisationRecord{}, false, &identityConflict{rec}
				}
				if kind == "suppliers" {
					var canonical string
					check, ce := s.orgTx(ctx, actor, archiveID, "view")
					if ce != nil {
						return OrganisationRecord{}, false, ce
					}
					ce = check.QueryRowContext(ctx, `SELECT identity_key FROM invoyis.suppliers WHERE archive_id=$1 AND id=$2`, archiveID, rec.ID).Scan(&canonical)
					check.Rollback()
					if ce != nil {
						return OrganisationRecord{}, false, ce
					}
					if canonical != key {
						return OrganisationRecord{}, false, &identityConflict{rec}
					}
				}
				return rec, false, nil
			}
		}
		return OrganisationRecord{}, false, err
	}
	if err = s.audit(ctx, tx, archiveID, actor, "organisation_"+kind+"_created", nil, map[string]any{"recordId": id}); err != nil {
		return OrganisationRecord{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return OrganisationRecord{}, false, err
	}
	return OrganisationRecord{id, display, false}, true, nil
}
func (s *Service) GetOrganisation(ctx context.Context, actor int64, archiveID, kind, id string) (OrganisationRecord, error) {
	table := orgTable(kind)
	if table == "" || !uuidPattern.MatchString(id) {
		return OrganisationRecord{}, ErrInvalid
	}
	tx, err := s.orgTx(ctx, actor, archiveID, "view")
	if err != nil {
		return OrganisationRecord{}, err
	}
	defer tx.Rollback()
	var rec OrganisationRecord
	err = tx.QueryRowContext(ctx, `SELECT id,display_name,archived_at IS NOT NULL FROM invoyis.`+table+` WHERE archive_id=$1 AND id=$2`, archiveID, id).Scan(&rec.ID, &rec.DisplayName, &rec.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, ErrNotFound
	}
	return rec, err
}
func (s *Service) ListOrganisation(ctx context.Context, actor int64, archiveID, kind string, includeArchived bool) ([]OrganisationRecord, error) {
	table := orgTable(kind)
	if table == "" {
		return nil, ErrInvalid
	}
	tx, err := s.orgTx(ctx, actor, archiveID, "view")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if includeArchived {
		if _, _, err = s.membership(ctx, tx, archiveID, actor, "settings"); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,display_name,archived_at IS NOT NULL FROM invoyis.`+table+` WHERE archive_id=$1 AND ($2 OR archived_at IS NULL) ORDER BY display_name,id`, archiveID, includeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []OrganisationRecord{}
	for rows.Next() {
		var v OrganisationRecord
		if err = rows.Scan(&v.ID, &v.DisplayName, &v.Archived); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Service) ChangeOrganisation(ctx context.Context, actor int64, archiveID, kind, id, operation, name string) (OrganisationRecord, error) {
	table := orgTable(kind)
	if table == "" || !uuidPattern.MatchString(id) {
		return OrganisationRecord{}, ErrInvalid
	}
	var display, key, broad string
	var err error
	if operation == "rename" {
		display, key, broad, err = organisationKeys(name)
		if err != nil {
			return OrganisationRecord{}, err
		}
	} else if operation != "archive" && operation != "restore" {
		return OrganisationRecord{}, ErrInvalid
	}
	tx, err := s.orgTx(ctx, actor, archiveID, "settings")
	if err != nil {
		return OrganisationRecord{}, err
	}
	defer tx.Rollback()
	var rec OrganisationRecord
	var oldKey string
	err = tx.QueryRowContext(ctx, `SELECT id,display_name,archived_at IS NOT NULL,identity_key FROM invoyis.`+table+` WHERE archive_id=$1 AND id=$2 FOR UPDATE`, archiveID, id).Scan(&rec.ID, &rec.DisplayName, &rec.Archived, &oldKey)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, ErrNotFound
	}
	if err != nil {
		return rec, err
	}
	changed := false
	switch operation {
	case "rename":
		if rec.Archived {
			return rec, ErrConflict
		}
		changed = rec.DisplayName != display || oldKey != key
		if changed {
			if kind == "suppliers" && oldKey != key {
				var target string
				e := tx.QueryRowContext(ctx, `SELECT supplier_id FROM invoyis.supplier_identity_keys WHERE archive_id=$1 AND identity_key=$2 FOR UPDATE`, archiveID, key).Scan(&target)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return rec, e
				}
				if target != "" && target != id {
					var other OrganisationRecord
					if e = tx.QueryRowContext(ctx, `SELECT id,display_name,archived_at IS NOT NULL FROM invoyis.suppliers WHERE archive_id=$1 AND id=$2`, archiveID, target).Scan(&other.ID, &other.DisplayName, &other.Archived); e != nil {
						return rec, e
					}
					return rec, &identityConflict{other}
				}
				if target == id {
					if _, e = tx.ExecContext(ctx, `DELETE FROM invoyis.supplier_identity_keys WHERE archive_id=$1 AND identity_key=$2 AND kind='alias'`, archiveID, key); e != nil {
						return rec, e
					}
				}
			}
			_, err = tx.ExecContext(ctx, `UPDATE invoyis.`+table+` SET display_name=$3,identity_key=$4,suggestion_key=$5 WHERE archive_id=$1 AND id=$2`, archiveID, id, display, key, broad)
			rec.DisplayName = display
		}
	case "archive":
		changed = !rec.Archived
		if changed {
			_, err = tx.ExecContext(ctx, `UPDATE invoyis.`+table+` SET archived_at=now() WHERE archive_id=$1 AND id=$2`, archiveID, id)
			rec.Archived = true
		}
	case "restore":
		changed = rec.Archived
		if changed {
			_, err = tx.ExecContext(ctx, `UPDATE invoyis.`+table+` SET archived_at=NULL WHERE archive_id=$1 AND id=$2`, archiveID, id)
			rec.Archived = false
		}
	}
	if orgUnique(err) {
		tx.Rollback()
		other, e := s.orgLookupKey(ctx, actor, archiveID, kind, key)
		if e != nil {
			return OrganisationRecord{}, e
		}
		return OrganisationRecord{}, &identityConflict{other}
	}
	if err != nil {
		return OrganisationRecord{}, err
	}
	if changed {
		if err = s.audit(ctx, tx, archiveID, actor, "organisation_"+kind+"_"+operation, nil, map[string]any{"recordId": id}); err != nil {
			return rec, err
		}
	}
	return rec, tx.Commit()
}

func (s *Service) organisationHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	archiveID, kind, id := r.PathValue("archiveId"), r.PathValue("kind"), r.PathValue("id")
	if orgTable(kind) == "" {
		writeFailure(w, ErrNotFound, requestID)
		return
	}
	if operation := r.PathValue("operation"); operation != "" && operation != "archive" && operation != "restore" {
		writeFailure(w, ErrNotFound, requestID)
		return
	}
	var rec OrganisationRecord
	var items []OrganisationRecord
	var err error
	status := http.StatusOK
	switch {
	case r.Method == "GET" && id == "":
		items, err = s.ListOrganisation(r.Context(), actor, archiveID, kind, r.URL.Query().Get("includeArchived") == "true")
	case r.Method == "GET":
		rec, err = s.GetOrganisation(r.Context(), actor, archiveID, kind, id)
	case r.Method == "POST" && id == "":
		var body struct {
			DisplayName string `json:"displayName"`
		}
		if err = decodeJSON(w, r, &body); err == nil {
			var created bool
			rec, created, err = s.CreateOrganisation(r.Context(), actor, archiveID, kind, body.DisplayName)
			if created {
				status = http.StatusCreated
			}
		}
	case r.Method == "PATCH":
		var body struct {
			DisplayName string `json:"displayName"`
		}
		if err = decodeJSON(w, r, &body); err == nil {
			rec, err = s.ChangeOrganisation(r.Context(), actor, archiveID, kind, id, "rename", body.DisplayName)
		}
	case r.Method == "POST" && id != "":
		rec, err = s.ChangeOrganisation(r.Context(), actor, archiveID, kind, id, r.PathValue("operation"), "")
	default:
		err = ErrNotFound
	}
	if err != nil {
		var conflict *identityConflict
		if errors.As(err, &conflict) {
			writeOrganisationConflict(w, requestID, conflict.Record)
			return
		}
		writeFailure(w, err, requestID)
		return
	}
	if items != nil {
		writeJSON(w, status, requestID, map[string]any{"items": items})
	} else {
		writeJSON(w, status, requestID, rec)
	}
}
func writeOrganisationConflict(w http.ResponseWriter, requestID string, rec OrganisationRecord) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(409)
	body := map[string]any{"type": "about:blank", "title": "Identity reserved", "status": 409, "code": "IDENTITY_RESERVED", "requestId": requestID}
	if rec.ID != "" {
		body["recordId"] = rec.ID
		body["archived"] = rec.Archived
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Service) supplierSuggestionsHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	items, err := s.SuggestSuppliers(r.Context(), actor, r.PathValue("archiveId"), r.URL.Query().Get("name"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"items": items})
}
func (s *Service) confirmSupplierAliasHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	created, err := s.ConfirmSupplierAlias(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), body.Name)
	if err != nil {
		var conflict *identityConflict
		if errors.As(err, &conflict) {
			writeOrganisationConflict(w, requestID, conflict.Record)
		} else {
			writeFailure(w, err, requestID)
		}
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"created": created})
}
func (s *Service) reassignSupplierAliasHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct {
		Name               string `json:"name"`
		ExpectedSupplierID string `json:"expectedSupplierId"`
		TargetSupplierID   string `json:"targetSupplierId"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	err := s.ReassignSupplierAlias(r.Context(), actor, r.PathValue("archiveId"), body.Name, body.ExpectedSupplierID, body.TargetSupplierID)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"reassigned": true})
}
