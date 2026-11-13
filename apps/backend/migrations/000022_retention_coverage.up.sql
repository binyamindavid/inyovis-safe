-- Coverage is immutable release evidence. Existing releases have no declarations,
-- so their current decisions remain unknown until a new release is activated.
CREATE TABLE invoyis.retention_coverage (
 coverage_id text PRIMARY KEY,
 rule_set_version text NOT NULL REFERENCES invoyis.retention_rule_sets(version),
 jurisdiction text NOT NULL CHECK (jurisdiction ~ '^[A-Z]{2}$'),
 entity_scope text NOT NULL CHECK (entity_scope <> ''),
 document_scope text NOT NULL CHECK (document_scope <> ''),
 effective_from date NOT NULL,
 effective_until date,
 selection_date text NOT NULL CHECK (selection_date IN ('issue_date','financial_year_end')),
 status text NOT NULL CHECK (status IN ('complete','excluded')),
 source_reference text NOT NULL CHECK (source_reference <> ''),
 CHECK (effective_until IS NULL OR effective_until > effective_from)
);
CREATE TRIGGER retention_coverage_immutable BEFORE UPDATE OR DELETE ON invoyis.retention_coverage FOR EACH ROW EXECUTE FUNCTION invoyis.retention_immutable();
CREATE TRIGGER retention_coverage_insert_guard BEFORE INSERT ON invoyis.retention_coverage FOR EACH ROW EXECUTE FUNCTION invoyis.retention_rule_insert_guard();

-- Exact and wildcard scopes can overlap. Different date bases can match the
-- same document even when their calendar windows do not overlap.
CREATE FUNCTION invoyis.validate_retention_coverage(target_version text) RETURNS void
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
CREATE FUNCTION invoyis.retention_coverage_publication_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.version IS DISTINCT FROM OLD.version THEN
  PERFORM invoyis.validate_retention_coverage(NEW.version);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER retention_coverage_publication_guard BEFORE UPDATE ON invoyis.retention_active_rule_set
FOR EACH ROW EXECUTE FUNCTION invoyis.retention_coverage_publication_guard();
-- The evaluator semantics changed even for releases without declarations.
-- Existing snapshots stay immutable but must not remain current after upgrade.
INSERT INTO invoyis.retention_queue(archive_id,document_id)
SELECT d.archive_id,d.id FROM invoyis.documents d
JOIN invoyis.archives a ON a.id=d.archive_id
WHERE a.kind='business' AND d.state='ready' AND d.clearance='cleared'
 AND d.current_confirmed_revision_id IS NOT NULL
ON CONFLICT(archive_id,document_id) DO UPDATE
SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT ON invoyis.retention_coverage TO invoyis_archive_runtime;
 END IF;
END $$;
