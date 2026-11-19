-- Physical disposal is opt-in and is never driven by Trash expiry.
ALTER TABLE invoyis.documents ADD COLUMN shred_requested_at timestamptz;
CREATE FUNCTION invoyis.shredded_document_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.shred_requested_at IS NOT NULL THEN RAISE EXCEPTION 'shredded document is immutable' USING ERRCODE='22023'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER shredded_document_immutable BEFORE UPDATE ON invoyis.documents
 FOR EACH ROW EXECUTE FUNCTION invoyis.shredded_document_immutable();
CREATE FUNCTION invoyis.reject_hold_after_shred() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,invoyis AS $$
DECLARE requested timestamptz;
BEGIN
 SELECT shred_requested_at INTO requested FROM invoyis.documents
 WHERE archive_id=NEW.archive_id AND id=NEW.document_id FOR KEY SHARE;
 IF requested IS NOT NULL THEN RAISE EXCEPTION 'document shredding was authorised' USING ERRCODE='22023'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER reject_hold_after_shred BEFORE INSERT ON invoyis.document_holds
 FOR EACH ROW EXECUTE FUNCTION invoyis.reject_hold_after_shred();
CREATE POLICY document_not_shredded ON invoyis.documents AS RESTRICTIVE FOR SELECT
 USING (shred_requested_at IS NULL OR current_setting('invoyis.include_shredded',true)='on');

CREATE TABLE invoyis.document_shreds (
 archive_id uuid NOT NULL, document_id uuid NOT NULL,
 actor_id bigint NOT NULL REFERENCES invoyis.accounts(id),
 requested_at timestamptz NOT NULL DEFAULT now(),
 confirmed_revision_id uuid NOT NULL, decision_id uuid,
 keep_through date, evidence jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','failed','verified')),
 verified_at timestamptz, last_error text,
 PRIMARY KEY (archive_id,document_id),
 FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.documents(archive_id,id)
);
CREATE TABLE invoyis.shred_objects (
 archive_id uuid NOT NULL, document_id uuid NOT NULL,
 stored_object_id uuid NOT NULL REFERENCES invoyis.stored_objects(id),
 storage_key text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','failed','verified')),
 attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_token uuid, lease_until timestamptz,
 verified_at timestamptz, last_error text,
 PRIMARY KEY (archive_id,document_id,stored_object_id),
 FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.document_shreds(archive_id,document_id)
);
CREATE INDEX shred_objects_due ON invoyis.shred_objects(next_attempt_at) WHERE status IN ('pending','failed');
-- No later canonicalisation may attach a fresh document to an object scheduled
-- for disposal. The original reference remains for the shredded document.
CREATE FUNCTION invoyis.reject_disposing_original() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM invoyis.shred_objects WHERE stored_object_id=NEW.stored_object_id)
 THEN RAISE EXCEPTION 'original is being disposed' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER reject_disposing_original BEFORE INSERT OR UPDATE OF stored_object_id
 ON invoyis.submissions FOR EACH ROW EXECUTE FUNCTION invoyis.reject_disposing_original();
ALTER TABLE invoyis.document_shreds ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.shred_objects ENABLE ROW LEVEL SECURITY;
CREATE POLICY document_shreds_read ON invoyis.document_shreds FOR SELECT USING
 (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY shred_objects_read ON invoyis.shred_objects FOR SELECT USING
 (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
CREATE FUNCTION invoyis.shred_export_blocked(target_archive uuid,target_document uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT invoyis.current_archive_id()=target_archive AND invoyis.can_shred(target_archive) AND EXISTS(
  SELECT 1 FROM invoyis.archive_exports WHERE archive_id=target_archive
   AND (state='building' OR (state='ready' AND items @> jsonb_build_array(jsonb_build_object('documentId',target_document)))))
$$;
REVOKE ALL ON FUNCTION invoyis.shred_export_blocked(uuid,uuid) FROM PUBLIC;

CREATE FUNCTION invoyis.authorize_document_shred(target_archive uuid,target_document uuid,
 expected_name text, expected_revision uuid, expected_decision uuid, expected_fingerprint text)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE docrow record; decision record; object_id uuid; object_key text; keep_date date;
BEGIN
 IF invoyis.current_archive_id() IS DISTINCT FROM target_archive OR NOT invoyis.can_shred(target_archive)
 THEN RAISE EXCEPTION 'shred permission denied' USING ERRCODE='42501'; END IF;
 SELECT a.kind,d.current_confirmed_revision_id AS revision,d.clearance,d.state,d.trashed_at,d.shred_requested_at,
  s.original_filename,s.stored_object_id INTO docrow
 FROM invoyis.documents d JOIN invoyis.archives a ON a.id=d.archive_id
 JOIN invoyis.submissions s ON s.id=d.submission_id
 WHERE d.archive_id=target_archive AND d.id=target_document FOR UPDATE OF d;
 IF NOT FOUND THEN RAISE EXCEPTION 'document not found' USING ERRCODE='02000'; END IF;
 IF docrow.shred_requested_at IS NOT NULL THEN RETURN 'pending'; END IF;
 IF docrow.original_filename IS DISTINCT FROM expected_name OR docrow.revision IS DISTINCT FROM expected_revision
 OR docrow.clearance<>'cleared' OR docrow.state<>'ready' THEN RETURN 'document_changed'; END IF;
 IF docrow.trashed_at IS NOT NULL THEN RETURN 'in_trash'; END IF;
 IF EXISTS(SELECT 1 FROM invoyis.document_holds WHERE archive_id=target_archive AND document_id=target_document AND released_at IS NULL)
 THEN RETURN 'active_hold'; END IF;
 IF EXISTS(SELECT 1 FROM invoyis.processing_jobs WHERE archive_id=target_archive AND document_id=target_document AND state IN ('pending','running'))
 OR EXISTS(SELECT 1 FROM invoyis.preview_claims WHERE archive_id=target_archive AND document_id=target_document AND expires_at>now())
 THEN RETURN 'running_job'; END IF;
 -- Building exports have not recorded their items yet; all must finish first.
 IF EXISTS(SELECT 1 FROM invoyis.archive_exports WHERE archive_id=target_archive AND state='building')
 OR EXISTS(SELECT 1 FROM invoyis.archive_exports WHERE archive_id=target_archive AND state='ready'
   AND items @> jsonb_build_array(jsonb_build_object('documentId',target_document)))
 THEN RETURN 'active_export'; END IF;
 IF docrow.kind='business' THEN
  SELECT id,outcome,confirmed_revision_id,fingerprint INTO decision
  FROM invoyis.retention_evaluations WHERE archive_id=target_archive AND document_id=target_document
  ORDER BY created_at DESC,id DESC LIMIT 1;
  IF NOT FOUND OR decision.id IS DISTINCT FROM expected_decision
   OR decision.confirmed_revision_id IS DISTINCT FROM expected_revision
   OR decision.fingerprint IS DISTINCT FROM expected_fingerprint THEN RETURN 'retention_stale'; END IF;
  IF decision.outcome->>'status'<>'calculated' OR decision.outcome->>'keepThrough' IS NULL
  THEN RETURN 'retention_unresolved'; END IF;
  IF (decision.outcome->>'keepThrough')::date >= (now() AT TIME ZONE 'UTC')::date
  THEN RETURN 'keep_through_date'; END IF;
  keep_date:=(decision.outcome->>'keepThrough')::date;
 ELSIF docrow.kind='personal' THEN
  IF expected_decision IS NOT NULL THEN RETURN 'personal_reminder_not_authorization'; END IF;
 ELSE RETURN 'archive_kind'; END IF;
 SELECT id,storage_key INTO object_id,object_key FROM invoyis.stored_objects
 WHERE archive_id=target_archive AND id=docrow.stored_object_id FOR UPDATE;
 IF NOT FOUND THEN RETURN 'original_missing'; END IF;
 UPDATE invoyis.documents SET shred_requested_at=now() WHERE archive_id=target_archive AND id=target_document;
 INSERT INTO invoyis.document_shreds(archive_id,document_id,actor_id,confirmed_revision_id,decision_id,keep_through,evidence)
 VALUES(target_archive,target_document,invoyis.current_account_id(),expected_revision,expected_decision,
  keep_date,
  jsonb_build_object('archiveKind',docrow.kind,'fingerprint',expected_fingerprint,'originalObjectId',object_id));
 DELETE FROM invoyis.preview_derivatives WHERE archive_id=target_archive AND document_id=target_document;
 IF NOT EXISTS(SELECT 1 FROM invoyis.submissions s JOIN invoyis.documents other ON other.submission_id=s.id
   WHERE s.stored_object_id=object_id AND other.id<>target_document AND other.shred_requested_at IS NULL)
 THEN INSERT INTO invoyis.shred_objects(archive_id,document_id,stored_object_id,storage_key)
  VALUES(target_archive,target_document,object_id,object_key);
 ELSE UPDATE invoyis.document_shreds SET status='verified',verified_at=now()
  WHERE archive_id=target_archive AND document_id=target_document; END IF;
 RETURN 'pending';
END $$;
REVOKE ALL ON FUNCTION invoyis.authorize_document_shred(uuid,uuid,text,uuid,uuid,text) FROM PUBLIC;
CREATE FUNCTION invoyis.claim_shred_object()
RETURNS TABLE(archive_id uuid,document_id uuid,stored_object_id uuid,storage_key text,token uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 RETURN QUERY WITH candidate AS (
  SELECT o.archive_id,o.document_id,o.stored_object_id FROM invoyis.shred_objects o
  WHERE o.status IN ('pending','failed') AND o.next_attempt_at<=now()
  AND (o.lease_until IS NULL OR o.lease_until<now())
  ORDER BY o.next_attempt_at,o.document_id LIMIT 1 FOR UPDATE SKIP LOCKED
 ), claimed AS (
  UPDATE invoyis.shred_objects o SET lease_token=gen_random_uuid(),lease_until=now()+interval '2 minutes',
   attempts=o.attempts+1,status='pending'
  FROM candidate c WHERE o.archive_id=c.archive_id AND o.document_id=c.document_id
   AND o.stored_object_id=c.stored_object_id
  RETURNING o.archive_id,o.document_id,o.stored_object_id,o.storage_key,o.lease_token
 ) SELECT c.archive_id,c.document_id,c.stored_object_id,c.storage_key,c.lease_token FROM claimed c;
END $$;
CREATE FUNCTION invoyis.shred_object_safe(target_archive uuid,target_document uuid,target_object uuid,owned_token uuid)
RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT EXISTS(SELECT 1 FROM invoyis.shred_objects o JOIN invoyis.stored_objects so
  ON so.id=o.stored_object_id AND so.archive_id=o.archive_id AND so.storage_key=o.storage_key
  WHERE o.archive_id=target_archive AND o.document_id=target_document
   AND o.stored_object_id=target_object AND o.lease_token=owned_token AND o.lease_until>now()
   AND NOT EXISTS(SELECT 1 FROM invoyis.submissions sub JOIN invoyis.documents d ON d.submission_id=sub.id
    WHERE sub.stored_object_id=target_object AND d.id<>target_document AND d.shred_requested_at IS NULL))
$$;
CREATE FUNCTION invoyis.finish_shred_object(target_archive uuid,target_document uuid,target_object uuid,
 owned_token uuid, verified boolean, failure text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE changed integer;
BEGIN
 UPDATE invoyis.shred_objects SET status=CASE WHEN verified THEN 'verified' ELSE 'failed' END,
  verified_at=CASE WHEN verified THEN now() ELSE NULL END,
  last_error=CASE WHEN verified THEN NULL ELSE left(failure,200) END,
  next_attempt_at=now()+make_interval(secs=>least(3600,power(2,least(attempts,11))::integer)),
  lease_token=NULL,lease_until=NULL
 WHERE archive_id=target_archive AND document_id=target_document AND stored_object_id=target_object
 AND lease_token=owned_token AND lease_until>now();
 GET DIAGNOSTICS changed=ROW_COUNT;
 IF changed<>1 THEN RETURN false; END IF;
 UPDATE invoyis.document_shreds s SET status=CASE WHEN verified THEN 'verified' ELSE 'failed' END,
  verified_at=CASE WHEN verified THEN now() ELSE NULL END,last_error=CASE WHEN verified THEN NULL ELSE left(failure,200) END
 WHERE s.archive_id=target_archive AND s.document_id=target_document
 AND NOT EXISTS(SELECT 1 FROM invoyis.shred_objects o WHERE o.archive_id=s.archive_id AND o.document_id=s.document_id AND o.status<>'verified');
 IF NOT verified THEN UPDATE invoyis.document_shreds SET status='failed',last_error=left(failure,200)
  WHERE archive_id=target_archive AND document_id=target_document; END IF;
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION invoyis.claim_shred_object(),invoyis.shred_object_safe(uuid,uuid,uuid,uuid),invoyis.finish_shred_object(uuid,uuid,uuid,uuid,boolean,text) FROM PUBLIC;
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
 GRANT SELECT ON invoyis.document_shreds,invoyis.shred_objects TO invoyis_archive_runtime;
 GRANT EXECUTE ON FUNCTION invoyis.authorize_document_shred(uuid,uuid,text,uuid,uuid,text) TO invoyis_archive_runtime;
 GRANT EXECUTE ON FUNCTION invoyis.claim_shred_object(),invoyis.finish_shred_object(uuid,uuid,uuid,uuid,boolean,text) TO invoyis_archive_runtime;
 GRANT EXECUTE ON FUNCTION invoyis.shred_object_safe(uuid,uuid,uuid,uuid) TO invoyis_archive_runtime;
 GRANT EXECUTE ON FUNCTION invoyis.shred_export_blocked(uuid,uuid) TO invoyis_archive_runtime;
END IF; END $$;
