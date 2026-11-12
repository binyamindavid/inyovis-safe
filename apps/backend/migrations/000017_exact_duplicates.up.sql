-- Existing document and receipt IDs remain stable. Only redundant stored-object
-- rows are folded into the earliest original for each archive and verified hash.
-- The old storage keys remain private for the operational cleanup in #30.
ALTER TABLE invoyis.submissions DROP CONSTRAINT submissions_stored_object_id_key;
WITH canonical AS (
 SELECT id,archive_id,sha256,
        first_value(id) OVER (PARTITION BY archive_id,sha256 ORDER BY created_at,id) AS keep_id
 FROM invoyis.stored_objects
), moved AS (
 UPDATE invoyis.submissions s SET stored_object_id=c.keep_id
 FROM canonical c WHERE s.stored_object_id=c.id AND c.id<>c.keep_id
 RETURNING c.id
)
DELETE FROM invoyis.stored_objects o WHERE o.id IN (SELECT id FROM moved);

ALTER TABLE invoyis.stored_objects ADD CONSTRAINT stored_objects_archive_sha256_key UNIQUE (archive_id,sha256);
ALTER TABLE invoyis.stored_objects ADD CONSTRAINT stored_objects_archive_id_key UNIQUE (archive_id,id);
ALTER TABLE invoyis.submissions ADD CONSTRAINT submissions_object_same_archive
 FOREIGN KEY (archive_id,stored_object_id) REFERENCES invoyis.stored_objects(archive_id,id);
ALTER TABLE invoyis.upload_receipts DROP CONSTRAINT upload_receipts_document_id_key;
