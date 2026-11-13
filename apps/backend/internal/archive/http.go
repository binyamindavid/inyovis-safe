package archive

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
)

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	RequestID string `json:"requestId"`
}

func writeProblem(w http.ResponseWriter, status int, code, title, requestID string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: title, Status: status, Code: code, RequestID: requestID})
}

func writeJSON(w http.ResponseWriter, status int, requestID string, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeFailure(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, sql.ErrNoRows):
		writeProblem(w, 404, "NOT_FOUND", "Not Found", requestID)
	case errors.Is(err, ErrForbidden):
		writeProblem(w, 403, "FORBIDDEN", "Forbidden", requestID)
	case errors.Is(err, ErrStagedObjectMissing):
		writeProblem(w, 409, "UPLOAD_STAGED_OBJECT_MISSING", "Staged object missing", requestID)
	case errors.Is(err, ErrConflict), errors.Is(err, ErrIntegrity):
		writeProblem(w, 409, "CONFLICT", "Conflict", requestID)
	case errors.Is(err, ErrRateLimited):
		var limited *rateLimitFailure
		if errors.As(err, &limited) {
			w.Header().Set("Retry-After", strconv.FormatInt(int64((limited.retryAfter+time.Second-1)/time.Second), 10))
		}
		writeProblem(w, 429, "RATE_LIMITED", "Too Many Requests", requestID)
	case errors.Is(err, ErrInvalid):
		writeProblem(w, 422, "VALIDATION_ERROR", "Invalid request", requestID)
	default:
		writeProblem(w, 500, "INTERNAL_ERROR", "Internal Server Error", requestID)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, body any) error {
	return decodeJSONLimited(w, r, body, 4096)
}

func decodeJSONLimited(w http.ResponseWriter, r *http.Request, body any, maxBytes int64) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return ErrInvalid
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/archives/{archiveId}/organisation/{kind}", s.Auth.Guard(false, s.organisationHTTP))
	mux.HandleFunc("POST /v1/archives/{archiveId}/organisation/{kind}", s.Auth.Guard(true, s.organisationHTTP))
	mux.HandleFunc("GET /v1/archives/{archiveId}/organisation/{kind}/{id}", s.Auth.Guard(false, s.organisationHTTP))
	mux.HandleFunc("PATCH /v1/archives/{archiveId}/organisation/{kind}/{id}", s.Auth.Guard(true, s.organisationHTTP))
	mux.HandleFunc("POST /v1/archives/{archiveId}/organisation/{kind}/{id}/{operation}", s.Auth.Guard(true, s.organisationHTTP))
	mux.HandleFunc("GET /v1/archives/{archiveId}/supplier-suggestions", s.Auth.Guard(false, s.supplierSuggestionsHTTP))
	mux.HandleFunc("POST /v1/archives/{archiveId}/suppliers/{id}/aliases", s.Auth.Guard(true, s.confirmSupplierAliasHTTP))
	mux.HandleFunc("POST /v1/archives/{archiveId}/supplier-aliases/reassign", s.Auth.Guard(true, s.reassignSupplierAliasHTTP))
	mux.HandleFunc("GET /v1/archives/{archiveId}/documents", s.Auth.Guard(false, s.listDocumentStatuses))
	mux.HandleFunc("GET /v1/archives/{archiveId}/duplicate-candidates", s.Auth.Guard(false, s.listDuplicateCandidatesHTTP))
	mux.HandleFunc("POST /v1/archives/{archiveId}/duplicate-candidates/{id}/resolve", s.Auth.Guard(true, s.resolveDuplicateHTTP))
	mux.HandleFunc("GET /v1/archives/{archiveId}/document-counts", s.Auth.Guard(false, s.countDocuments))
	mux.HandleFunc("GET /v1/archives/{archiveId}/document-folders", s.Auth.Guard(false, s.listDocumentFolders))
	mux.HandleFunc("GET /v1/archives/{archiveId}/suppliers/{id}/documents", s.Auth.Guard(false, s.listSupplierDocuments))
	mux.HandleFunc("GET /v1/archives/{archiveId}/documents/{id}/status", s.Auth.Guard(false, s.getDocumentStatus))
	mux.HandleFunc("GET /v1/archives/{archiveId}/documents/{id}/review", s.Auth.Guard(false, s.getReview))
	mux.HandleFunc("GET /v1/archives/{archiveId}/documents/{id}/preview/{page}", s.Auth.Guard(false, s.getPreview))
	mux.HandleFunc("GET /v1/archives/{archiveId}/documents/{id}/original", s.Auth.Guard(false, s.getOriginal))
	mux.HandleFunc("POST /v1/archives/{archiveId}/exports", s.Auth.Guard(true, s.createExport))
	mux.HandleFunc("GET /v1/archives/{archiveId}/exports/{id}", s.Auth.Guard(false, s.getExport))
	mux.HandleFunc("PUT /v1/archives/{archiveId}/documents/{id}/review", s.Auth.Guard(true, s.putReview))
	mux.HandleFunc("POST /v1/archives/{archiveId}/upload-sessions", s.Auth.Guard(true, s.createUploadSession))
	mux.HandleFunc("POST /v1/archives/{archiveId}/upload-sessions/{id}/complete", s.Auth.Guard(true, s.completeUploadSession))
	mux.HandleFunc("GET /v1/archives", s.Auth.Guard(false, s.listArchives))
	mux.HandleFunc("POST /v1/archives", s.Auth.Guard(true, s.createArchive))
	mux.HandleFunc("GET /v1/archives/{archiveId}", s.Auth.Guard(false, s.getArchive))
	mux.HandleFunc("PATCH /v1/archives/{archiveId}", s.Auth.Guard(true, s.updateArchive))
	mux.HandleFunc("GET /v1/archives/{archiveId}/members", s.Auth.Guard(false, s.listMembers))
	mux.HandleFunc("PATCH /v1/archives/{archiveId}/members/{accountId}", s.Auth.Guard(true, s.changeMember))
	mux.HandleFunc("DELETE /v1/archives/{archiveId}/members/{accountId}", s.Auth.Guard(true, s.removeMember))
	mux.HandleFunc("GET /v1/archives/{archiveId}/invitations", s.Auth.Guard(false, s.listInvitations))
	mux.HandleFunc("POST /v1/archives/{archiveId}/invitations", s.Auth.Guard(true, s.createInvitation))
	mux.HandleFunc("DELETE /v1/archives/{archiveId}/invitations/{invitationId}", s.Auth.Guard(true, s.revokeInvitation))
	mux.HandleFunc("POST /v1/invitations/accept", s.Auth.Guard(true, s.acceptInvitation))
	mux.HandleFunc("GET /v1/account/language", s.Auth.Guard(false, s.getLanguage))
	mux.HandleFunc("PUT /v1/account/language", s.Auth.Guard(true, s.setLanguage))
}

func (s *Service) getDocumentStatus(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	accountID, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	item, err := s.DocumentStatus(r.Context(), accountID, r.PathValue("archiveId"), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, item)
}
func (s *Service) listDocumentStatuses(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	accountID, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	f, cursor, err := parseDocumentFilters(r.URL.Query(), false)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	items, err := s.BrowseDocuments(r.Context(), accountID, r.PathValue("archiveId"), "", f, cursor)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, items)
}

func (s *Service) countDocuments(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	accountID, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeFailure(w, ErrInvalid, requestID)
		return
	}
	counts, err := s.CountDocuments(r.Context(), accountID, r.PathValue("archiveId"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, counts)
}

func (s *Service) listDocumentFolders(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	accountID, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	q := r.URL.Query()
	if len(q) != 1 || len(q["by"]) != 1 {
		writeFailure(w, ErrInvalid, requestID)
		return
	}
	folders, err := s.ListDocumentFolders(r.Context(), accountID, r.PathValue("archiveId"), q.Get("by"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, folders)
}

func (s *Service) listSupplierDocuments(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	accountID, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	f, cursor, err := parseDocumentFilters(r.URL.Query(), true)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	items, err := s.BrowseDocuments(r.Context(), accountID, r.PathValue("archiveId"), r.PathValue("id"), f, cursor)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, items)
}

func (s *Service) createUploadSession(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	accountID, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var claim UploadClaim
	if err := decodeJSON(w, r, &claim); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	session, err := s.CreateUpload(r.Context(), accountID, r.PathValue("archiveId"), claim)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusCreated, requestID, session)
}

func (s *Service) completeUploadSession(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	accountID, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	receipt, err := s.CompleteUpload(r.Context(), accountID, r.PathValue("archiveId"), r.PathValue("id"), false)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, receipt)
}

func (s *Service) account(w http.ResponseWriter, r *http.Request, requestID string) (int64, string, bool) {
	return s.sessionAccount(w, r, requestID, false)
}

func (s *Service) sessionAccount(w http.ResponseWriter, r *http.Request, requestID string, allowPending bool) (int64, string, bool) {
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		writeProblem(w, 401, "UNAUTHORIZED", "Unauthorized", requestID)
		return 0, "", false
	}
	var id int64
	var email string
	if allowPending {
		id, email, _, _, err = s.Auth.SessionIdentity(r.Context(), cookie.Value)
	} else {
		id, email, _, err = s.Auth.SessionAccount(r.Context(), cookie.Value)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, 401, "UNAUTHORIZED", "Unauthorized", requestID)
		return 0, "", false
	}
	if err != nil {
		writeFailure(w, err, requestID)
		return 0, "", false
	}
	return id, email, true
}

const authCookieName = "__Host-invoyis_session"

func (s *Service) listArchives(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	items, err := s.List(r.Context(), id)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"items": items})
}

func (s *Service) createArchive(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct{ Kind, Name, Jurisdiction string }
	if err := decodeJSON(w, r, &body); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	item, err := s.Create(r.Context(), id, body.Kind, body.Name, body.Jurisdiction)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 201, requestID, item)
}

func (s *Service) getArchive(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	item, err := s.Get(r.Context(), id, r.PathValue("archiveId"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, item)
}

func (s *Service) updateArchive(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct{ Name, Jurisdiction string }
	if err := decodeJSON(w, r, &body); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	item, err := s.Update(r.Context(), id, r.PathValue("archiveId"), body.Name, body.Jurisdiction)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, item)
}

func (s *Service) listMembers(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	items, err := s.Members(r.Context(), id, r.PathValue("archiveId"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"items": items})
}

func (s *Service) changeMember(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	targetID, err := strconv.ParseInt(r.PathValue("accountId"), 10, 64)
	if err != nil {
		writeFailure(w, ErrNotFound, requestID)
		return
	}
	var body struct {
		Role          string `json:"role"`
		CanBulkExport bool   `json:"canBulkExport"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	if err := s.ChangeMember(r.Context(), id, r.PathValue("archiveId"), targetID, body.Role, body.CanBulkExport); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"accountId": targetID, "role": body.Role, "canBulkExport": body.CanBulkExport})
}

func (s *Service) removeMember(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	targetID, err := strconv.ParseInt(r.PathValue("accountId"), 10, 64)
	if err != nil {
		writeFailure(w, ErrNotFound, requestID)
		return
	}
	if err := s.RemoveMember(r.Context(), id, r.PathValue("archiveId"), targetID); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(204)
}

func (s *Service) listInvitations(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	items, err := s.Invitations(r.Context(), id, r.PathValue("archiveId"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"items": items})
}

func (s *Service) createInvitation(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct{ Email, Role string }
	if err := decodeJSON(w, r, &body); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	item, err := s.Invite(r.Context(), id, r.PathValue("archiveId"), body.Email, body.Role, r.Header.Get("Accept-Language"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 201, requestID, item)
}

func (s *Service) revokeInvitation(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	if err := s.RevokeInvitation(r.Context(), id, r.PathValue("archiveId"), r.PathValue("invitationId")); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(204)
}

func (s *Service) acceptInvitation(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.sessionAccount(w, r, requestID, true)
	if !ok {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	item, err := s.Accept(r.Context(), id, body.Token)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, item)
}

func (s *Service) getLanguage(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	language, err := s.Language(r.Context(), id)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]string{"language": language})
}

func (s *Service) setLanguage(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	id, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct {
		Language string `json:"language"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	if err := s.SetLanguage(r.Context(), id, body.Language); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]string{"language": body.Language})
}
