-- Activation is explicit. A new set is populated while inactive, then published
-- by changing this singleton pointer in the same transaction as its validation.
CREATE TABLE invoyis.retention_active_rule_set (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 version text NOT NULL REFERENCES invoyis.retention_rule_sets(version)
);
INSERT INTO invoyis.retention_active_rule_set(singleton,version) VALUES (true,'empty-v1');
CREATE FUNCTION invoyis.retention_rule_insert_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
DECLARE active_version text;
BEGIN
 SELECT version INTO active_version FROM invoyis.retention_active_rule_set WHERE singleton=true FOR SHARE;
 IF NEW.rule_set_version=active_version THEN RAISE EXCEPTION 'cannot edit an active retention rule set'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER retention_rule_insert_guard BEFORE INSERT ON invoyis.retention_rule_versions FOR EACH ROW EXECUTE FUNCTION invoyis.retention_rule_insert_guard();
CREATE FUNCTION invoyis.lock_retention_active_set() RETURNS text LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT version FROM invoyis.retention_active_rule_set WHERE singleton=true FOR SHARE
$$;
REVOKE EXECUTE ON FUNCTION invoyis.lock_retention_active_set() FROM PUBLIC;
CREATE FUNCTION invoyis.lock_retention_inputs(target_archive uuid,target_document uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE locked uuid; active_version text;
BEGIN
 IF target_archive IS DISTINCT FROM invoyis.current_archive_id()
    OR NOT invoyis.is_archive_member(target_archive) THEN RETURN false; END IF;
 SELECT id INTO locked FROM invoyis.archives WHERE id=target_archive FOR SHARE;
 IF locked IS NULL THEN RETURN false; END IF;
 SELECT id INTO locked FROM invoyis.documents WHERE archive_id=target_archive AND id=target_document FOR SHARE;
 IF locked IS NULL THEN RETURN false; END IF;
 SELECT version INTO active_version FROM invoyis.retention_active_rule_set WHERE singleton=true FOR SHARE;
 RETURN active_version IS NOT NULL;
END $$;
REVOKE EXECUTE ON FUNCTION invoyis.lock_retention_inputs(uuid,uuid) FROM PUBLIC;

CREATE TABLE invoyis.retention_document_facts (
 archive_id uuid NOT NULL,
 document_id uuid NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 entity_scope text,
 tax_year integer CHECK (tax_year BETWEEN 1000 AND 9999),
 tax_year_basis text,
 actor_id bigint NOT NULL REFERENCES invoyis.accounts(id),
 recorded_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(archive_id,document_id),
 FOREIGN KEY(archive_id,document_id) REFERENCES invoyis.documents(archive_id,id),
 CHECK ((tax_year IS NULL) = (tax_year_basis IS NULL))
);
CREATE TABLE invoyis.retention_document_facts_history (
 archive_id uuid NOT NULL, document_id uuid NOT NULL, version bigint NOT NULL,
 evidence jsonb NOT NULL, actor_id bigint NOT NULL REFERENCES invoyis.accounts(id),
 recorded_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(archive_id,document_id,version),
 FOREIGN KEY(archive_id,document_id) REFERENCES invoyis.retention_document_facts(archive_id,document_id)
);
CREATE TRIGGER retention_document_facts_history_immutable BEFORE UPDATE OR DELETE ON invoyis.retention_document_facts_history FOR EACH ROW EXECUTE FUNCTION invoyis.retention_immutable();
ALTER TABLE invoyis.retention_document_facts ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.retention_document_facts_history ENABLE ROW LEVEL SECURITY;
CREATE POLICY retention_document_facts_read ON invoyis.retention_document_facts FOR SELECT USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY retention_document_facts_write ON invoyis.retention_document_facts FOR ALL USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id)) WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id) AND actor_id=invoyis.current_account_id());
CREATE POLICY retention_document_facts_history_read ON invoyis.retention_document_facts_history FOR SELECT USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY retention_document_facts_history_write ON invoyis.retention_document_facts_history FOR INSERT WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id) AND actor_id=invoyis.current_account_id());

CREATE TABLE invoyis.retention_queue (
 archive_id uuid NOT NULL, document_id uuid NOT NULL,
 generation bigint NOT NULL DEFAULT 1,
 lease_until timestamptz,
 queued_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(archive_id,document_id),
 FOREIGN KEY(archive_id,document_id) REFERENCES invoyis.documents(archive_id,id)
);
CREATE FUNCTION invoyis.queue_retention(target_archive uuid,target_document uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 INSERT INTO invoyis.retention_queue(archive_id,document_id) VALUES(target_archive,target_document)
 ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,queued_at=now()
$$;
REVOKE EXECUTE ON FUNCTION invoyis.queue_retention(uuid,uuid) FROM PUBLIC;
CREATE FUNCTION invoyis.retention_document_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF TG_OP='INSERT' OR NEW.current_confirmed_revision_id IS DISTINCT FROM OLD.current_confirmed_revision_id
    OR NEW.clearance IS DISTINCT FROM OLD.clearance OR NEW.state IS DISTINCT FROM OLD.state THEN
  IF NEW.current_confirmed_revision_id IS NOT NULL THEN PERFORM invoyis.queue_retention(NEW.archive_id,NEW.id); END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER retention_document_changed AFTER INSERT OR UPDATE ON invoyis.documents FOR EACH ROW EXECUTE FUNCTION invoyis.retention_document_changed();
CREATE FUNCTION invoyis.retention_archive_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.jurisdiction IS DISTINCT FROM OLD.jurisdiction OR NEW.kind IS DISTINCT FROM OLD.kind THEN
  INSERT INTO invoyis.retention_queue(archive_id,document_id)
  SELECT NEW.id,d.id FROM invoyis.documents d WHERE d.archive_id=NEW.id AND d.current_confirmed_revision_id IS NOT NULL
  ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,queued_at=now();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER retention_archive_changed AFTER UPDATE ON invoyis.archives FOR EACH ROW EXECUTE FUNCTION invoyis.retention_archive_changed();
CREATE FUNCTION invoyis.retention_event_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 INSERT INTO invoyis.retention_queue(archive_id,document_id)
 SELECT NEW.archive_id,d.id FROM invoyis.documents d
 JOIN invoyis.retention_document_facts f ON f.archive_id=d.archive_id AND f.document_id=d.id AND f.tax_year=NEW.tax_year
 WHERE d.archive_id=NEW.archive_id AND d.current_confirmed_revision_id IS NOT NULL
 ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,queued_at=now();
 RETURN NEW;
END $$;
CREATE TRIGGER retention_event_changed AFTER INSERT OR UPDATE ON invoyis.archive_tax_years FOR EACH ROW EXECUTE FUNCTION invoyis.retention_event_changed();
CREATE FUNCTION invoyis.retention_facts_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN PERFORM invoyis.queue_retention(NEW.archive_id,NEW.document_id); RETURN NEW; END $$;
CREATE TRIGGER retention_facts_changed AFTER INSERT OR UPDATE ON invoyis.retention_document_facts FOR EACH ROW EXECUTE FUNCTION invoyis.retention_facts_changed();
CREATE FUNCTION invoyis.retention_set_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.version IS DISTINCT FROM OLD.version THEN
  INSERT INTO invoyis.retention_queue(archive_id,document_id)
  SELECT d.archive_id,d.id FROM invoyis.documents d WHERE d.current_confirmed_revision_id IS NOT NULL
  ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,queued_at=now();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER retention_set_changed AFTER UPDATE ON invoyis.retention_active_rule_set FOR EACH ROW EXECUTE FUNCTION invoyis.retention_set_changed();

-- Migration backfill: documents confirmed before this feature receive work.
INSERT INTO invoyis.retention_queue(archive_id,document_id)
SELECT archive_id,id FROM invoyis.documents WHERE current_confirmed_revision_id IS NOT NULL;

CREATE FUNCTION invoyis.claim_retention_work(batch_size integer)
RETURNS TABLE(archive_id uuid,document_id uuid,generation bigint,actor_id bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 WITH claimed AS (
  SELECT q.archive_id,q.document_id FROM invoyis.retention_queue q
  WHERE q.lease_until IS NULL OR q.lease_until<now()
  ORDER BY q.queued_at,q.archive_id,q.document_id FOR UPDATE SKIP LOCKED LIMIT LEAST(GREATEST(batch_size,0),32)
 ), leased AS (
  UPDATE invoyis.retention_queue q SET lease_until=now()+interval '1 minute'
  FROM claimed c WHERE q.archive_id=c.archive_id AND q.document_id=c.document_id
  RETURNING q.archive_id,q.document_id,q.generation
 )
 SELECT l.archive_id,l.document_id,l.generation,m.account_id FROM leased l
 JOIN LATERAL (SELECT account_id FROM invoyis.archive_memberships m WHERE m.archive_id=l.archive_id AND m.role='admin' ORDER BY account_id LIMIT 1) m ON true
$$;
CREATE FUNCTION invoyis.ack_retention_work(target_archive uuid,target_document uuid,target_generation bigint) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 DELETE FROM invoyis.retention_queue WHERE archive_id=target_archive AND document_id=target_document AND generation=target_generation;
 RETURN FOUND;
END $$;
REVOKE EXECUTE ON FUNCTION invoyis.claim_retention_work(integer),invoyis.ack_retention_work(uuid,uuid,bigint) FROM PUBLIC;

DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT,INSERT,UPDATE ON invoyis.retention_document_facts TO invoyis_archive_runtime;
  GRANT SELECT,INSERT ON invoyis.retention_document_facts_history TO invoyis_archive_runtime;
  GRANT SELECT ON invoyis.retention_active_rule_set TO invoyis_archive_runtime;
  GRANT EXECUTE ON FUNCTION invoyis.claim_retention_work(integer),invoyis.ack_retention_work(uuid,uuid,bigint) TO invoyis_archive_runtime;
  GRANT EXECUTE ON FUNCTION invoyis.lock_retention_active_set() TO invoyis_archive_runtime;
  GRANT EXECUTE ON FUNCTION invoyis.lock_retention_inputs(uuid,uuid) TO invoyis_archive_runtime;
 END IF;
END $$;
