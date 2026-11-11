package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/rendering"
)

type previewSource struct {
	key, digest string
	size        int64
	cached      []byte
	count       int
}

const previewRenderSlots = 4
const previewAdvisoryNamespace = 701862

// Session-scoped advisory locks bound renders across API instances and archives.
// A lost process releases its connection and slot automatically.
func (s *Service) acquireRenderSlot(ctx context.Context) (*sql.Conn, int, error) {
	for {
		conn, err := s.DB.Conn(ctx)
		if err != nil {
			return nil, 0, err
		}
		for slot := 1; slot <= previewRenderSlots; slot++ {
			var acquired bool
			err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1,$2)`, previewAdvisoryNamespace, slot).Scan(&acquired)
			if err != nil {
				_ = conn.Close()
				return nil, 0, err
			}
			if acquired {
				return conn, slot, nil
			}
		}
		_ = conn.Close()
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func releaseRenderSlot(conn *sql.Conn, slot int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var released bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1,$2)`, previewAdvisoryNamespace, slot).Scan(&released); err != nil || !released {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}

func (s *Service) previewLookup(ctx context.Context, actor int64, archiveID, documentID string, page int) (previewSource, error) {
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return previewSource{}, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "view"); e != nil {
		if errors.Is(e, ErrForbidden) {
			return previewSource{}, ErrNotFound
		}
		return previewSource{}, e
	}
	var source previewSource
	var clearance string
	e = tx.QueryRowContext(ctx, `SELECT d.clearance,o.storage_key,o.sha256,o.byte_count FROM invoyis.documents d JOIN invoyis.submissions sub ON sub.id=d.submission_id JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id WHERE d.archive_id=$1 AND d.id=$2`, archiveID, documentID).Scan(&clearance, &source.key, &source.digest, &source.size)
	if errors.Is(e, sql.ErrNoRows) {
		return previewSource{}, ErrNotFound
	}
	if e != nil {
		return previewSource{}, e
	}
	if clearance != "cleared" {
		return previewSource{}, ErrForbidden
	}
	e = tx.QueryRowContext(ctx, `SELECT image,page_count FROM invoyis.preview_derivatives WHERE document_id=$1 AND input_sha256=$2 AND page=$3 AND renderer_version=$4`, documentID, source.digest, page, rendering.RendererVersion).Scan(&source.cached, &source.count)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return previewSource{}, e
	}
	return source, tx.Commit()
}
func (s *Service) claimPreview(ctx context.Context, actor int64, archiveID, documentID, digest string, page int, owner string, charge bool) (bool, error) {
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var clearance string
	e = tx.QueryRowContext(ctx, `SELECT clearance FROM invoyis.documents WHERE archive_id=$1 AND id=$2 FOR UPDATE`, archiveID, documentID).Scan(&clearance)
	if errors.Is(e, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if e != nil {
		return false, e
	}
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "view"); e != nil {
		return false, e
	}
	if clearance != "cleared" {
		return false, ErrForbidden
	}
	// The document lock also serialises the cache publication in finishPreview.
	var cached bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM invoyis.preview_derivatives WHERE document_id=$1 AND input_sha256=$2 AND page=$3 AND renderer_version=$4)`, documentID, digest, page, rendering.RendererVersion).Scan(&cached)
	if e != nil {
		return false, e
	}
	if cached {
		return false, tx.Commit()
	}
	if charge {
		var count int
		e = tx.QueryRowContext(ctx, `INSERT INTO invoyis.preview_rate(archive_id,window_start,requests) VALUES($1,date_trunc('minute',now()),1) ON CONFLICT(archive_id,window_start) DO UPDATE SET requests=invoyis.preview_rate.requests+1 WHERE invoyis.preview_rate.requests<60 RETURNING requests`, archiveID).Scan(&count)
		if errors.Is(e, sql.ErrNoRows) {
			return false, ErrRateLimited
		}
		if e != nil {
			return false, e
		}
	}
	var holder string
	e = tx.QueryRowContext(ctx, `INSERT INTO invoyis.preview_claims(archive_id,document_id,input_sha256,page,renderer_version,owner,expires_at) VALUES($1,$2,$3,$4,$5,$6,now()+interval '30 seconds') ON CONFLICT(document_id,input_sha256,page,renderer_version) DO UPDATE SET owner=$6,expires_at=now()+interval '30 seconds' WHERE invoyis.preview_claims.expires_at<now() RETURNING owner`, archiveID, documentID, digest, page, rendering.RendererVersion, owner).Scan(&holder)
	if errors.Is(e, sql.ErrNoRows) {
		return false, tx.Commit()
	}
	if e != nil {
		return false, e
	}
	return holder == owner, tx.Commit()
}
func (s *Service) finishPreview(ctx context.Context, actor int64, archiveID, documentID, digest string, page int, owner string, image []byte, count int) error {
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if image != nil {
		var clearance string
		e = tx.QueryRowContext(ctx, `SELECT clearance FROM invoyis.documents WHERE archive_id=$1 AND id=$2 FOR UPDATE`, archiveID, documentID).Scan(&clearance)
		if e != nil {
			return e
		}
		if clearance != "cleared" {
			return ErrForbidden
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.preview_derivatives(archive_id,document_id,input_sha256,page,renderer_version,image,page_count) SELECT $1,$2,$3,$4,$5,$6,$8 WHERE EXISTS(SELECT 1 FROM invoyis.preview_claims WHERE document_id=$2 AND input_sha256=$3 AND page=$4 AND renderer_version=$5 AND owner=$7) ON CONFLICT DO NOTHING`, archiveID, documentID, digest, page, rendering.RendererVersion, image, owner, count)
		if e != nil {
			return e
		}
		// Keep the private archive cache below 100 MiB, oldest pages first.
		_, e = tx.ExecContext(ctx, `DELETE FROM invoyis.preview_derivatives WHERE (document_id,input_sha256,page,renderer_version) IN (SELECT document_id,input_sha256,page,renderer_version FROM (SELECT document_id,input_sha256,page,renderer_version,sum(octet_length(image)) OVER (ORDER BY created_at DESC,document_id DESC,page DESC) AS cumulative FROM invoyis.preview_derivatives WHERE archive_id=$1) q WHERE cumulative>100000000)`, archiveID)
		if e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `DELETE FROM invoyis.preview_claims WHERE document_id=$1 AND input_sha256=$2 AND page=$3 AND renderer_version=$4 AND owner=$5`, documentID, digest, page, rendering.RendererVersion, owner)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) PreviewPage(ctx context.Context, actor int64, archiveID, documentID string, page int) ([]byte, int, error) {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return nil, 0, ErrNotFound
	}
	if page < 1 || page > 10 {
		return nil, 0, rendering.ErrPreviewTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, 26*time.Second)
	defer cancel()
	source, e := s.previewLookup(ctx, actor, archiveID, documentID, page)
	if e != nil {
		return nil, 0, e
	}
	if len(source.cached) > 0 {
		return source.cached, source.count, nil
	}
	owner, e := newID()
	if e != nil {
		return nil, 0, e
	}
	for attempt := 0; attempt < 60; attempt++ {
		image, count, claimed, e := s.attemptPreview(ctx, actor, archiveID, documentID, page, owner, attempt == 0, source)
		if e != nil {
			if errors.Is(e, context.DeadlineExceeded) {
				return nil, 0, rendering.ErrPreviewTimeout
			}
			return nil, 0, e
		}
		if claimed {
			return image, count, nil
		}
		source, e = s.previewLookup(ctx, actor, archiveID, documentID, page)
		if e != nil {
			return nil, 0, e
		}
		if len(source.cached) > 0 {
			return source.cached, source.count, nil
		}
		select {
		case <-ctx.Done():
			return nil, 0, rendering.ErrPreviewTimeout
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, 0, rendering.ErrPreviewTimeout
}
func (s *Service) attemptPreview(ctx context.Context, actor int64, archiveID, documentID string, page int, owner string, charge bool, source previewSource) ([]byte, int, bool, error) {
	conn, slot, err := s.acquireRenderSlot(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	defer releaseRenderSlot(conn, slot)
	claimed, err := s.claimPreview(ctx, actor, archiveID, documentID, source.digest, page, owner, charge)
	if err != nil || !claimed {
		return nil, 0, false, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.finishPreview(cleanup, actor, archiveID, documentID, source.digest, page, owner, nil, 0)
	}()
	if s.Storage == nil {
		return nil, 0, false, rendering.ErrPreviewUnsupported
	}
	data, err := s.Storage.Read(ctx, source.key, source.size)
	if err != nil {
		return nil, 0, false, err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != source.digest {
		return nil, 0, false, ErrIntegrity
	}
	render := s.RenderPage
	if render == nil {
		render = rendering.RenderIsolated
	}
	image, count, err := render(ctx, data, page)
	if err != nil {
		return nil, 0, false, err
	}
	if err = s.finishPreview(ctx, actor, archiveID, documentID, source.digest, page, owner, image, count); err != nil {
		return nil, 0, false, err
	}
	latest, err := s.previewLookup(ctx, actor, archiveID, documentID, page)
	if err != nil {
		return nil, 0, false, err
	}
	if len(latest.cached) == 0 {
		return nil, 0, false, rendering.ErrPreviewUnsupported
	}
	return latest.cached, latest.count, true, nil
}
func (s *Service) getPreview(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	page, e := strconv.Atoi(r.PathValue("page"))
	if e != nil {
		writeFailure(w, ErrInvalid, requestID)
		return
	}
	data, count, e := s.PreviewPage(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), page)
	if e != nil {
		if errors.Is(e, rendering.ErrPreviewUnsupported) || errors.Is(e, rendering.ErrPreviewTooLarge) || errors.Is(e, rendering.ErrPreviewTimeout) {
			writeProblem(w, 422, rendering.PreviewFailureCode(e), "Preview unavailable", requestID)
			return
		}
		writeFailure(w, e, requestID)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Preview-Page-Cap", "10")
	w.Header().Set("X-Preview-Page-Count", strconv.Itoa(count))
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
