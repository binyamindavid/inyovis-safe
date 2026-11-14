-- Rows created before archive-kind scoping belong to the legacy Business
-- catalogue. Keep their immutable identities, including entity_scope='person'.
ALTER TABLE invoyis.retention_rule_versions
 ADD COLUMN archive_kind text NOT NULL DEFAULT 'business' CHECK (archive_kind IN ('business','personal'));
ALTER TABLE invoyis.retention_coverage
 ADD COLUMN archive_kind text NOT NULL DEFAULT 'business' CHECK (archive_kind IN ('business','personal'));

CREATE OR REPLACE FUNCTION invoyis.validate_retention_coverage(target_version text) RETURNS void
LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM invoyis.retention_coverage a
  JOIN invoyis.retention_coverage b ON a.rule_set_version=b.rule_set_version AND a.coverage_id<b.coverage_id
  WHERE a.rule_set_version=target_version AND a.archive_kind=b.archive_kind AND a.jurisdiction=b.jurisdiction
   AND (a.entity_scope=b.entity_scope OR a.entity_scope='*' OR b.entity_scope='*')
   AND (a.document_scope=b.document_scope OR a.document_scope='*' OR b.document_scope='*')
   AND a.status<>b.status
   AND (a.selection_date<>b.selection_date OR (
     a.effective_from<COALESCE(b.effective_until,'infinity'::date)
     AND b.effective_from<COALESCE(a.effective_until,'infinity'::date)))
 ) THEN RAISE EXCEPTION 'contradictory retention coverage declarations'; END IF;
 IF EXISTS (
  SELECT 1 FROM invoyis.retention_coverage c WHERE c.rule_set_version=target_version AND c.status='complete'
  AND NOT EXISTS (SELECT 1 FROM invoyis.retention_rule_versions r WHERE r.rule_set_version=target_version
   AND r.active AND r.archive_kind=c.archive_kind AND r.jurisdiction=c.jurisdiction
   AND (r.entity_scope=c.entity_scope OR r.entity_scope='*')
   AND (r.document_scope=c.document_scope OR r.document_scope='*')
   AND r.selection_date=c.selection_date
   AND r.effective_from<COALESCE(c.effective_until,'infinity'::date)
   AND c.effective_from<COALESCE(r.effective_until,'infinity'::date))
 ) THEN RAISE EXCEPTION 'complete retention scope lacks a validated active rule version'; END IF;
END $$;

-- The evaluator's kind-aware fingerprint invalidates decisions from both kinds.
INSERT INTO invoyis.retention_queue(archive_id,document_id)
SELECT d.archive_id,d.id FROM invoyis.documents d JOIN invoyis.archives a ON a.id=d.archive_id
WHERE a.kind IN ('business','personal') AND d.current_confirmed_revision_id IS NOT NULL
ON CONFLICT(archive_id,document_id) DO UPDATE
SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
