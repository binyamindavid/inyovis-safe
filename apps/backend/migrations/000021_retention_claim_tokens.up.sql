-- A generation describes input changes, but it is not a lease identity: it can
-- repeat after a queue row is deleted and recreated. Every claim gets a fresh
-- token, and any new input clears the token through the queue upsert paths.
ALTER TABLE invoyis.retention_queue ADD COLUMN claim_token uuid;
CREATE OR REPLACE FUNCTION invoyis.queue_retention(target_archive uuid,target_document uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 INSERT INTO invoyis.retention_queue(archive_id,document_id) VALUES(target_archive,target_document)
 ON CONFLICT(archive_id,document_id) DO UPDATE
 SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now()
$$;
CREATE OR REPLACE FUNCTION invoyis.retention_archive_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.jurisdiction IS DISTINCT FROM OLD.jurisdiction OR NEW.kind IS DISTINCT FROM OLD.kind THEN
  INSERT INTO invoyis.retention_queue(archive_id,document_id)
  SELECT NEW.id,d.id FROM invoyis.documents d WHERE d.archive_id=NEW.id AND d.current_confirmed_revision_id IS NOT NULL
  ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION invoyis.retention_event_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 INSERT INTO invoyis.retention_queue(archive_id,document_id)
 SELECT NEW.archive_id,d.id FROM invoyis.documents d
 JOIN invoyis.retention_document_facts f ON f.archive_id=d.archive_id AND f.document_id=d.id AND f.tax_year=NEW.tax_year
 WHERE d.archive_id=NEW.archive_id AND d.current_confirmed_revision_id IS NOT NULL
 ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION invoyis.retention_set_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.version IS DISTINCT FROM OLD.version THEN
  INSERT INTO invoyis.retention_queue(archive_id,document_id)
  SELECT d.archive_id,d.id FROM invoyis.documents d WHERE d.current_confirmed_revision_id IS NOT NULL
  ON CONFLICT(archive_id,document_id) DO UPDATE SET generation=invoyis.retention_queue.generation+1,lease_until=NULL,claim_token=NULL,queued_at=now();
 END IF;
 RETURN NEW;
END $$;
DROP FUNCTION invoyis.claim_retention_work(integer);
DROP FUNCTION invoyis.ack_retention_work(uuid,uuid,bigint);
CREATE FUNCTION invoyis.claim_retention_work(batch_size integer)
RETURNS TABLE(archive_id uuid,document_id uuid,generation bigint,claim_token uuid,actor_id bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 WITH claimed AS (
  SELECT q.archive_id,q.document_id FROM invoyis.retention_queue q
  WHERE q.lease_until IS NULL OR q.lease_until<now()
  ORDER BY q.queued_at,q.archive_id,q.document_id FOR UPDATE SKIP LOCKED LIMIT LEAST(GREATEST(batch_size,0),32)
 ), leased AS (
  UPDATE invoyis.retention_queue q SET lease_until=now()+interval '1 minute',claim_token=gen_random_uuid()
  FROM claimed c WHERE q.archive_id=c.archive_id AND q.document_id=c.document_id
  RETURNING q.archive_id,q.document_id,q.generation,q.claim_token
 )
 SELECT l.archive_id,l.document_id,l.generation,l.claim_token,m.account_id FROM leased l
 JOIN LATERAL (SELECT account_id FROM invoyis.archive_memberships m WHERE m.archive_id=l.archive_id AND m.role='admin' ORDER BY account_id LIMIT 1) m ON true
$$;
CREATE FUNCTION invoyis.ack_retention_work(target_archive uuid,target_document uuid,target_token uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 DELETE FROM invoyis.retention_queue WHERE archive_id=target_archive AND document_id=target_document AND claim_token=target_token;
 RETURN FOUND;
END $$;
REVOKE EXECUTE ON FUNCTION invoyis.claim_retention_work(integer),invoyis.ack_retention_work(uuid,uuid,uuid) FROM PUBLIC;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT EXECUTE ON FUNCTION invoyis.claim_retention_work(integer),invoyis.ack_retention_work(uuid,uuid,uuid) TO invoyis_archive_runtime;
 END IF;
END $$;
