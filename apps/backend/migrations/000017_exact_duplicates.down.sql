-- Reversing convergence would require inventing new originals and changing
-- historical submission links. Refuse rollback once duplicate links exist.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.submissions GROUP BY stored_object_id HAVING count(*)>1)
    OR EXISTS (SELECT 1 FROM invoyis.upload_receipts GROUP BY document_id HAVING count(*)>1) THEN
  RAISE EXCEPTION 'cannot reverse exact-duplicate convergence with shared originals';
 END IF;
END $$;
ALTER TABLE invoyis.upload_receipts DROP CONSTRAINT receipts_document_same_archive;
ALTER TABLE invoyis.upload_receipts DROP CONSTRAINT receipts_submission_same_archive;
ALTER TABLE invoyis.upload_receipts DROP CONSTRAINT receipts_session_same_archive;
ALTER TABLE invoyis.upload_receipts ADD CONSTRAINT upload_receipts_document_id_key UNIQUE (document_id);
ALTER TABLE invoyis.processing_jobs DROP CONSTRAINT jobs_document_same_archive;
ALTER TABLE invoyis.documents DROP CONSTRAINT documents_submission_same_archive;
ALTER TABLE invoyis.submissions DROP CONSTRAINT submissions_object_same_archive;
ALTER TABLE invoyis.submissions DROP CONSTRAINT submissions_session_same_archive;
ALTER TABLE invoyis.stored_objects DROP CONSTRAINT stored_objects_session_same_archive;
ALTER TABLE invoyis.submissions DROP CONSTRAINT submissions_archive_id_key;
ALTER TABLE invoyis.submissions ADD CONSTRAINT submissions_stored_object_id_key UNIQUE (stored_object_id);
ALTER TABLE invoyis.stored_objects DROP CONSTRAINT stored_objects_archive_id_key;
ALTER TABLE invoyis.stored_objects DROP CONSTRAINT stored_objects_archive_sha256_key;
