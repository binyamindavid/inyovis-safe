CREATE TABLE invoyis.document_holds (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL, document_id uuid NOT NULL,
 reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
 placed_by bigint NOT NULL REFERENCES invoyis.accounts(id), placed_at timestamptz NOT NULL DEFAULT now(),
 released_by bigint REFERENCES invoyis.accounts(id), released_at timestamptz,
 CHECK ((released_by IS NULL) = (released_at IS NULL)),
 FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.documents(archive_id,id)
);
CREATE INDEX document_holds_active ON invoyis.document_holds(archive_id,document_id) WHERE released_at IS NULL;
ALTER TABLE invoyis.document_holds ENABLE ROW LEVEL SECURITY;
CREATE POLICY document_holds_read ON invoyis.document_holds FOR SELECT USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY document_holds_insert ON invoyis.document_holds FOR INSERT WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id) AND placed_by=invoyis.current_account_id());
CREATE POLICY document_holds_update ON invoyis.document_holds FOR UPDATE USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id) AND released_at IS NULL) WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id) AND released_by=invoyis.current_account_id() AND released_at IS NOT NULL);
CREATE FUNCTION invoyis.hold_release_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.id IS DISTINCT FROM OLD.id OR NEW.archive_id IS DISTINCT FROM OLD.archive_id OR NEW.document_id IS DISTINCT FROM OLD.document_id OR NEW.reason IS DISTINCT FROM OLD.reason OR NEW.placed_by IS DISTINCT FROM OLD.placed_by OR NEW.placed_at IS DISTINCT FROM OLD.placed_at OR OLD.released_at IS NOT NULL OR NEW.released_at IS NULL OR NEW.released_by IS NULL THEN RAISE EXCEPTION 'hold history is immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER hold_release_guard BEFORE UPDATE ON invoyis.document_holds FOR EACH ROW EXECUTE FUNCTION invoyis.hold_release_guard();
CREATE TRIGGER hold_delete_guard BEFORE DELETE ON invoyis.document_holds FOR EACH ROW EXECUTE FUNCTION invoyis.retention_immutable();
CREATE TABLE invoyis.personal_reminders (
 archive_id uuid NOT NULL, document_id uuid NOT NULL, reminder_date date NOT NULL,
 actor_id bigint NOT NULL REFERENCES invoyis.accounts(id), updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (archive_id,document_id), FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.documents(archive_id,id)
);
ALTER TABLE invoyis.personal_reminders ENABLE ROW LEVEL SECURITY;
CREATE POLICY personal_reminders_read ON invoyis.personal_reminders FOR SELECT USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY personal_reminders_insert ON invoyis.personal_reminders FOR INSERT WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id) AND actor_id=invoyis.current_account_id());
CREATE POLICY personal_reminders_update ON invoyis.personal_reminders FOR UPDATE USING (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id)) WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id) AND actor_id=invoyis.current_account_id());
CREATE POLICY personal_reminders_delete ON invoyis.personal_reminders FOR DELETE USING (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON invoyis.document_holds,invoyis.personal_reminders TO invoyis_archive_runtime;
END IF; END $$;
-- Publication queues only documents whose possible rule or coverage scopes changed.
CREATE OR REPLACE FUNCTION invoyis.retention_set_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.version IS DISTINCT FROM OLD.version THEN
  WITH older AS (
   SELECT to_jsonb(r)-'rule_set_version' AS value FROM invoyis.retention_rule_versions r WHERE r.rule_set_version=OLD.version
   UNION SELECT to_jsonb(c)-'rule_set_version' FROM invoyis.retention_coverage c WHERE c.rule_set_version=OLD.version
  ), newer AS (
   SELECT to_jsonb(r)-'rule_set_version' AS value FROM invoyis.retention_rule_versions r WHERE r.rule_set_version=NEW.version
   UNION SELECT to_jsonb(c)-'rule_set_version' FROM invoyis.retention_coverage c WHERE c.rule_set_version=NEW.version
  ), added AS (SELECT value FROM newer EXCEPT SELECT value FROM older),
  removed AS (SELECT value FROM older EXCEPT SELECT value FROM newer),
  scopes AS (SELECT value->>'jurisdiction' AS country,value->>'entity_scope' AS entity,value->>'document_scope' AS document FROM added
   UNION SELECT value->>'jurisdiction',value->>'entity_scope',value->>'document_scope' FROM removed)
  INSERT INTO invoyis.retention_queue(archive_id,document_id)
  SELECT DISTINCT d.archive_id,d.id FROM invoyis.documents d
  JOIN invoyis.archives a ON a.id=d.archive_id
  JOIN invoyis.document_revisions r ON r.id=d.current_confirmed_revision_id
  LEFT JOIN invoyis.retention_document_facts f ON f.archive_id=d.archive_id AND f.document_id=d.id
  JOIN scopes s ON s.country=a.jurisdiction
   AND (s.entity='*' OR f.entity_scope IS NULL OR s.entity=f.entity_scope)
   AND (s.document='*' OR NULLIF(r.metadata->>'documentType','') IS NULL OR s.document=r.metadata->>'documentType')
  WHERE a.kind='business' AND d.state='ready' AND d.clearance='cleared'
  ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
 END IF;
 RETURN NEW;
END $$;
-- Re-evaluate existing Business snapshots once under the scope-specific fingerprint.
INSERT INTO invoyis.retention_queue(archive_id,document_id)
SELECT d.archive_id,d.id FROM invoyis.documents d JOIN invoyis.archives a ON a.id=d.archive_id
WHERE a.kind='business' AND d.state='ready' AND d.clearance='cleared' AND d.current_confirmed_revision_id IS NOT NULL
ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
-- A complete live scope needs a versioned active rule. Excluded scopes are
-- explicit evidence that no obligation applies and do not need a rule.
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
-- A new release can carry forward the same immutable rule/coverage identity.
-- This lets unchanged country/entity/document scopes keep their fingerprint.
ALTER TABLE invoyis.retention_rule_versions DROP CONSTRAINT retention_rule_versions_pkey;
ALTER TABLE invoyis.retention_rule_versions ADD PRIMARY KEY (rule_id,version,rule_set_version);
ALTER TABLE invoyis.retention_coverage DROP CONSTRAINT retention_coverage_pkey;
ALTER TABLE invoyis.retention_coverage ADD PRIMARY KEY (coverage_id,rule_set_version);
CREATE FUNCTION invoyis.retention_rule_identity_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.retention_rule_versions r WHERE r.rule_id=NEW.rule_id AND r.version=NEW.version
  AND to_jsonb(r)-'rule_set_version' <> to_jsonb(NEW)-'rule_set_version')
 THEN RAISE EXCEPTION 'rule identity changed across releases'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER retention_rule_identity_guard BEFORE INSERT ON invoyis.retention_rule_versions FOR EACH ROW EXECUTE FUNCTION invoyis.retention_rule_identity_guard();
CREATE FUNCTION invoyis.retention_coverage_identity_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.retention_coverage c WHERE c.coverage_id=NEW.coverage_id
  AND to_jsonb(c)-'rule_set_version' <> to_jsonb(NEW)-'rule_set_version')
 THEN RAISE EXCEPTION 'coverage identity changed across releases'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER retention_coverage_identity_guard BEFORE INSERT ON invoyis.retention_coverage FOR EACH ROW EXECUTE FUNCTION invoyis.retention_coverage_identity_guard();
