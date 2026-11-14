DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.retention_rule_versions WHERE archive_kind='personal')
 OR EXISTS (SELECT 1 FROM invoyis.retention_coverage WHERE archive_kind='personal')
 THEN RAISE EXCEPTION 'migration 26 contains Personal rule evidence; use a forward repair'; END IF;
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
 IF EXISTS (
  SELECT 1 FROM invoyis.retention_coverage c WHERE c.rule_set_version=target_version AND c.status='complete'
  AND NOT EXISTS (SELECT 1 FROM invoyis.retention_rule_versions r WHERE r.rule_set_version=target_version
   AND r.active AND r.jurisdiction=c.jurisdiction
   AND (r.entity_scope=c.entity_scope OR r.entity_scope='*')
   AND (r.document_scope=c.document_scope OR r.document_scope='*')
   AND r.selection_date=c.selection_date
   AND r.effective_from<COALESCE(c.effective_until,'infinity'::date)
   AND c.effective_from<COALESCE(r.effective_until,'infinity'::date))
 ) THEN RAISE EXCEPTION 'complete retention scope lacks a validated active rule version'; END IF;
END $$;
ALTER TABLE invoyis.retention_coverage DROP COLUMN archive_kind;
ALTER TABLE invoyis.retention_rule_versions DROP COLUMN archive_kind;
