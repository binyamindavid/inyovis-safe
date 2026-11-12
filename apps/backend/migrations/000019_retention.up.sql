-- Rule versions are immutable; activation creates a new rule-set release rather than mutating a version.
CREATE TABLE invoyis.retention_rule_sets (
 version text PRIMARY KEY,
 created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO invoyis.retention_rule_sets(version) VALUES ('empty-v1');
CREATE TABLE invoyis.retention_rule_versions (
 rule_id text NOT NULL, version integer NOT NULL CHECK (version > 0),
 rule_set_version text NOT NULL REFERENCES invoyis.retention_rule_sets(version),
 jurisdiction text NOT NULL CHECK (jurisdiction ~ '^[A-Z]{2}$'),
 entity_scope text NOT NULL, document_scope text NOT NULL,
 effective_from date NOT NULL, effective_until date,
 selection_date text NOT NULL CHECK (selection_date IN ('issue_date','financial_year_end')),
 source_reference text NOT NULL,
 trigger_event text NOT NULL CHECK (trigger_event IN ('issue_date','financial_year_end','filing_date')),
 calendar_years integer NOT NULL CHECK (calendar_years >= 0),
 active boolean NOT NULL DEFAULT false,
 PRIMARY KEY(rule_id,version),
 CHECK (effective_until IS NULL OR effective_until > effective_from)
);
CREATE FUNCTION invoyis.retention_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'retention history is immutable'; END $$;
CREATE TRIGGER retention_rules_immutable BEFORE UPDATE OR DELETE ON invoyis.retention_rule_versions FOR EACH ROW EXECUTE FUNCTION invoyis.retention_immutable();
CREATE TRIGGER retention_sets_immutable BEFORE UPDATE OR DELETE ON invoyis.retention_rule_sets FOR EACH ROW EXECUTE FUNCTION invoyis.retention_immutable();

CREATE TABLE invoyis.archive_tax_years (
 archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 tax_year integer NOT NULL CHECK (tax_year BETWEEN 1000 AND 9999),
 version bigint NOT NULL CHECK (version > 0),
 financial_year_end date,
 financial_year_end_source text,
 financial_year_end_actor bigint REFERENCES invoyis.accounts(id),
 financial_year_end_recorded_at timestamptz,
 filing_date date,
 filing_date_source text,
 filing_date_actor bigint REFERENCES invoyis.accounts(id),
 filing_date_recorded_at timestamptz,
 PRIMARY KEY(archive_id,tax_year),
 CHECK ((financial_year_end IS NULL) = (financial_year_end_source IS NULL)),
 CHECK ((filing_date IS NULL) = (filing_date_source IS NULL))
);
CREATE TABLE invoyis.archive_tax_year_history (
 archive_id uuid NOT NULL, tax_year integer NOT NULL, version bigint NOT NULL,
 evidence jsonb NOT NULL, actor_id bigint NOT NULL REFERENCES invoyis.accounts(id),
 recorded_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(archive_id,tax_year,version),
 FOREIGN KEY(archive_id,tax_year) REFERENCES invoyis.archive_tax_years(archive_id,tax_year)
);
CREATE TRIGGER tax_year_history_immutable BEFORE UPDATE OR DELETE ON invoyis.archive_tax_year_history FOR EACH ROW EXECUTE FUNCTION invoyis.retention_immutable();
ALTER TABLE invoyis.archive_tax_years ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.archive_tax_year_history ENABLE ROW LEVEL SECURITY;
CREATE POLICY tax_year_read ON invoyis.archive_tax_years FOR SELECT USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY tax_year_insert ON invoyis.archive_tax_years FOR INSERT WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
CREATE POLICY tax_year_update ON invoyis.archive_tax_years FOR UPDATE USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id)) WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
CREATE POLICY tax_year_history_read ON invoyis.archive_tax_year_history FOR SELECT USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY tax_year_history_insert ON invoyis.archive_tax_year_history FOR INSERT WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id) AND actor_id=invoyis.current_account_id());

CREATE TABLE invoyis.retention_evaluations (
 id uuid PRIMARY KEY,
 archive_id uuid NOT NULL, document_id uuid NOT NULL,
 confirmed_revision_id uuid NOT NULL,
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
 rule_set_version text NOT NULL REFERENCES invoyis.retention_rule_sets(version),
 inputs jsonb NOT NULL, outcome jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(archive_id,document_id,fingerprint),
 FOREIGN KEY(archive_id,document_id,confirmed_revision_id) REFERENCES invoyis.document_revisions(archive_id,document_id,id)
);
CREATE INDEX retention_evaluations_history ON invoyis.retention_evaluations(archive_id,document_id,created_at DESC,id DESC);
CREATE TRIGGER retention_evaluations_immutable BEFORE UPDATE OR DELETE ON invoyis.retention_evaluations FOR EACH ROW EXECUTE FUNCTION invoyis.retention_immutable();
ALTER TABLE invoyis.retention_evaluations ENABLE ROW LEVEL SECURITY;
CREATE POLICY retention_evaluations_read ON invoyis.retention_evaluations FOR SELECT USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY retention_evaluations_insert ON invoyis.retention_evaluations FOR INSERT WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT ON invoyis.retention_rule_sets,invoyis.retention_rule_versions TO invoyis_archive_runtime;
  GRANT SELECT,INSERT,UPDATE ON invoyis.archive_tax_years TO invoyis_archive_runtime;
  GRANT SELECT,INSERT ON invoyis.archive_tax_year_history,invoyis.retention_evaluations TO invoyis_archive_runtime;
 END IF;
END $$;
