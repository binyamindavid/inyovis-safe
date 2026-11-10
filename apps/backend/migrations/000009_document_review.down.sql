DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.document_revisions) THEN
  RAISE EXCEPTION 'review history exists; refusing to drop document revisions';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION invoyis.worker_complete(target uuid, owned_token uuid, input_digest text,
 version text, detected_profile text, fields jsonb, sources jsonb, notices jsonb)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE doc uuid; archive uuid; recorded_digest text;
BEGIN
 SELECT j.document_id,j.archive_id,o.sha256 INTO doc,archive,recorded_digest
 FROM invoyis.processing_jobs j JOIN invoyis.documents d ON d.id=j.document_id
 JOIN invoyis.submissions s ON s.id=d.submission_id JOIN invoyis.stored_objects o ON o.id=s.stored_object_id
 WHERE j.id=target AND j.state='running' AND j.lease_token=owned_token AND j.lease_expires_at>now()
 FOR UPDATE OF j;
 IF doc IS NULL THEN RETURN false; END IF;
 IF input_digest<>recorded_digest THEN RAISE EXCEPTION 'input digest differs from stored original'; END IF;
 INSERT INTO invoyis.extraction_runs(archive_id,document_id,stage,input_sha256,extractor_version,profile,candidates,provenance,warnings)
 VALUES(archive,doc,'document_xml',input_digest,version,detected_profile,fields,sources,notices)
 ON CONFLICT(document_id,stage,input_sha256,extractor_version) DO NOTHING;
 UPDATE invoyis.processing_jobs SET state='succeeded',failure_code=NULL,lease_token=NULL,lease_expires_at=NULL WHERE id=target;
 UPDATE invoyis.documents SET state='needs_review',clearance='cleared' WHERE id=doc;
 RETURN true;
END $$;


DROP TABLE invoyis.review_idempotency;
ALTER TABLE invoyis.documents DROP CONSTRAINT draft_revision_same_document;
ALTER TABLE invoyis.documents DROP CONSTRAINT confirmed_revision_same_document;
DROP TABLE invoyis.document_revisions;
ALTER TABLE invoyis.extraction_runs DROP CONSTRAINT extraction_runs_document_identity;
ALTER TABLE invoyis.documents DROP CONSTRAINT documents_archive_id_unique,
 DROP COLUMN latest_draft_revision_id,DROP COLUMN current_confirmed_revision_id,DROP COLUMN review_version;

