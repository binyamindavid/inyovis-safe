-- Keep the legal review alongside each immutable rule version. Existing test
-- releases are synthetic; publication of sourced rules requires an approval.
ALTER TABLE invoyis.retention_rule_versions
 ADD COLUMN exceptions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(exceptions)='array'),
 ADD COLUMN validation_record jsonb NOT NULL DEFAULT '{"status":"synthetic"}'::jsonb CHECK (jsonb_typeof(validation_record)='object');

CREATE FUNCTION invoyis.validate_retention_catalogue(target_version text) RETURNS void
LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM invoyis.retention_rule_versions r
  WHERE r.rule_set_version=target_version AND r.active
   AND (r.exceptions <> '[]'::jsonb
    OR ((r.validation_record->>'status' IS DISTINCT FROM 'synthetic'
      OR r.source_reference !~ '^synthetic')
     AND (r.validation_record->>'status' IS DISTINCT FROM 'approved'
      OR r.validation_record->>'scope_verified' IS DISTINCT FROM 'true'
      OR r.validation_record->>'starting_event_verified' IS DISTINCT FROM 'true'
      OR COALESCE(r.validation_record->>'reviewer','')=''
      OR COALESCE(r.validation_record->>'reviewed_at','')=''
      OR r.source_reference !~ '^https://')))
 ) THEN RAISE EXCEPTION 'retention rule lacks verified scope, starting event, official source or enforceable exceptions'; END IF;
END $$;

CREATE OR REPLACE FUNCTION invoyis.retention_coverage_publication_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.version IS DISTINCT FROM OLD.version THEN
  PERFORM invoyis.validate_retention_coverage(NEW.version);
  PERFORM invoyis.validate_retention_catalogue(NEW.version);
 END IF;
 RETURN NEW;
END $$;

-- Publication must revisit Personal documents as well as Business documents.
CREATE FUNCTION invoyis.retention_personal_set_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.version IS DISTINCT FROM OLD.version THEN
  INSERT INTO invoyis.retention_queue(archive_id,document_id)
  SELECT d.archive_id,d.id FROM invoyis.documents d
  JOIN invoyis.archives a ON a.id=d.archive_id
  WHERE a.kind='personal' AND d.current_confirmed_revision_id IS NOT NULL
  ON CONFLICT(archive_id,document_id) DO UPDATE
  SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER retention_personal_set_changed AFTER UPDATE ON invoyis.retention_active_rule_set
FOR EACH ROW EXECUTE FUNCTION invoyis.retention_personal_set_changed();
-- BEGIN evaluator version backfill
INSERT INTO invoyis.retention_queue(archive_id,document_id)
SELECT d.archive_id,d.id FROM invoyis.documents d JOIN invoyis.archives a ON a.id=d.archive_id
-- The evaluator fingerprint changed for both kinds in this release.
WHERE a.kind IN ('personal','business') AND d.current_confirmed_revision_id IS NOT NULL
ON CONFLICT(archive_id,document_id) DO UPDATE
SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
-- END evaluator version backfill
