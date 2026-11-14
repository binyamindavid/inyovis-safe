-- Historic holds, reminders and carried rule identities cannot be discarded.
-- Fail before changing any schema; repair a deployed release with a forward migration.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.document_holds)
 OR EXISTS (SELECT 1 FROM invoyis.personal_reminders)
 OR EXISTS (SELECT 1 FROM invoyis.retention_rule_versions GROUP BY rule_id,version HAVING count(*) > 1)
 OR EXISTS (SELECT 1 FROM invoyis.retention_coverage GROUP BY coverage_id HAVING count(*) > 1)
 THEN RAISE EXCEPTION 'migration 23 contains retention evidence; use a forward repair'; END IF;
END $$;
DROP TABLE invoyis.personal_reminders;
DROP TABLE invoyis.document_holds;
DROP FUNCTION invoyis.hold_release_guard();
CREATE OR REPLACE FUNCTION invoyis.retention_set_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.version IS DISTINCT FROM OLD.version THEN
  INSERT INTO invoyis.retention_queue(archive_id,document_id)
  SELECT d.archive_id,d.id FROM invoyis.documents d WHERE d.current_confirmed_revision_id IS NOT NULL
  ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION invoyis.validate_retention_coverage(target_version text) RETURNS void
LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM invoyis.retention_coverage a
  JOIN invoyis.retention_coverage b ON a.rule_set_version=b.rule_set_version AND a.coverage_id<b.coverage_id
  WHERE a.rule_set_version=target_version AND a.jurisdiction=b.jurisdiction
   AND (a.entity_scope=b.entity_scope OR a.entity_scope='*' OR b.entity_scope='*')
   AND (a.document_scope=b.document_scope OR a.document_scope='*' OR b.document_scope='*')
   AND a.status<>b.status
   AND (a.selection_date<>b.selection_date OR (
     a.effective_from<COALESCE(b.effective_until,'infinity'::date)
     AND b.effective_from<COALESCE(a.effective_until,'infinity'::date)))
 ) THEN RAISE EXCEPTION 'contradictory retention coverage declarations'; END IF;
END $$;
DROP TRIGGER retention_rule_identity_guard ON invoyis.retention_rule_versions;
DROP FUNCTION invoyis.retention_rule_identity_guard();
DROP TRIGGER retention_coverage_identity_guard ON invoyis.retention_coverage;
DROP FUNCTION invoyis.retention_coverage_identity_guard();
ALTER TABLE invoyis.retention_rule_versions DROP CONSTRAINT retention_rule_versions_pkey;
ALTER TABLE invoyis.retention_rule_versions ADD PRIMARY KEY (rule_id,version);
ALTER TABLE invoyis.retention_coverage DROP CONSTRAINT retention_coverage_pkey;
ALTER TABLE invoyis.retention_coverage ADD PRIMARY KEY (coverage_id);
