package archive

import "net/http"

func (s *Service) getRetentionDetails(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	v, e := s.RetentionDetails(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"))
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	writeJSON(w, 200, requestID, v)
}
func (s *Service) reassessRetention(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	a, d := r.PathValue("archiveId"), r.PathValue("id")
	tx, e := s.transaction(r.Context(), actor, a)
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	_, _, e = s.membership(r.Context(), tx, a, actor, "settings")
	tx.Rollback()
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	v, e := s.EvaluateRetention(r.Context(), actor, a, d)
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	writeJSON(w, 200, requestID, v)
}
func (s *Service) placeHold(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if e := decodeJSON(w, r, &body); e != nil {
		writeFailure(w, e, requestID)
		return
	}
	v, e := s.PlaceDocumentHold(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), body.Reason)
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	writeJSON(w, 201, requestID, v)
}
func (s *Service) releaseHold(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	e := s.ReleaseDocumentHold(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), r.PathValue("holdId"))
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	w.WriteHeader(204)
}
func (s *Service) putReminder(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct {
		Date *string `json:"date"`
	}
	if e := decodeJSON(w, r, &body); e != nil {
		writeFailure(w, e, requestID)
		return
	}
	e := s.SetPersonalReminder(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), body.Date)
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	w.WriteHeader(204)
}
func (s *Service) getShreddingReview(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	v, e := s.OpenShreddingReview(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"))
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	writeJSON(w, 200, requestID, v)
}
