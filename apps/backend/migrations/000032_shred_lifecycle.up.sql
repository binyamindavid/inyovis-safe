-- A shred retains only its actor, decision references, timing and verified
-- outcome. Document and upload rows are removed after every owned key is gone.
ALTER TABLE invoyis.document_shreds DROP CONSTRAINT document_shreds_archive_id_document_id_fkey;
ALTER TABLE invoyis.shred_objects DROP CONSTRAINT shred_objects_stored_object_id_fkey;
ALTER TABLE invoyis.shred_objects DROP CONSTRAINT shred_objects_pkey;
ALTER TABLE invoyis.shred_objects ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE invoyis.shred_objects ADD PRIMARY KEY(id);
ALTER TABLE invoyis.shred_objects ALTER COLUMN stored_object_id DROP NOT NULL;
ALTER TABLE invoyis.shred_objects ALTER COLUMN storage_key DROP NOT NULL;
ALTER TABLE invoyis.shred_objects ADD COLUMN upload_session_id uuid;
ALTER TABLE invoyis.shred_objects ADD COLUMN object_kind text NOT NULL DEFAULT 'original'
 CHECK (object_kind IN ('original','session_final','prepared','staging'));
ALTER TABLE invoyis.shred_objects ADD COLUMN key_fingerprint text;
ALTER TABLE invoyis.shred_objects DROP CONSTRAINT shred_objects_status_check;
ALTER TABLE invoyis.shred_objects ADD CONSTRAINT shred_objects_status_check
 CHECK (status IN ('pending','failed','verified','shared'));
CREATE UNIQUE INDEX shred_object_key_once ON invoyis.shred_objects(archive_id,document_id,storage_key)
 WHERE storage_key IS NOT NULL;

CREATE OR REPLACE FUNCTION invoyis.shredded_document_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.shred_requested_at IS NOT NULL AND NOT (
  NEW.current_confirmed_revision_id IS NULL AND NEW.latest_draft_revision_id IS NULL
  AND to_jsonb(NEW)-'current_confirmed_revision_id'-'latest_draft_revision_id'
   = to_jsonb(OLD)-'current_confirmed_revision_id'-'latest_draft_revision_id'
 ) THEN RAISE EXCEPTION 'shredded document is immutable' USING ERRCODE='22023'; END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION invoyis.retention_immutable() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF TG_OP='DELETE' AND TG_TABLE_NAME IN ('retention_evaluations','retention_document_facts_history','document_holds')
  AND EXISTS(SELECT 1 FROM invoyis.document_shreds s
   WHERE s.archive_id=OLD.archive_id AND s.document_id=OLD.document_id AND s.status='pending')
 THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'retention evidence is immutable';
END $$;

CREATE FUNCTION invoyis.finalize_document_shred(target_archive uuid,target_document uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE session_ids uuid[]; submission_ids uuid[]; canonical_session_ids uuid[]; doc_exists boolean;
BEGIN
 PERFORM 1 FROM invoyis.document_shreds WHERE archive_id=target_archive AND document_id=target_document
  AND status='pending' FOR UPDATE;
 IF NOT FOUND THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM invoyis.shred_objects WHERE archive_id=target_archive AND document_id=target_document
  AND status NOT IN ('verified','shared')) THEN RETURN false; END IF;
 SELECT array_agg(DISTINCT upload_session_id),array_agg(DISTINCT submission_id)
 INTO session_ids,submission_ids FROM invoyis.upload_receipts
 WHERE archive_id=target_archive AND document_id=target_document;
 SELECT array_agg(DISTINCT o.upload_session_id) INTO canonical_session_ids FROM invoyis.stored_objects o
 WHERE o.archive_id=target_archive AND EXISTS(SELECT 1 FROM invoyis.shred_objects k
  WHERE k.archive_id=target_archive AND k.document_id=target_document
   AND k.stored_object_id=o.id AND k.status='verified');
 SELECT EXISTS(SELECT 1 FROM invoyis.documents WHERE archive_id=target_archive AND id=target_document) INTO doc_exists;
 IF doc_exists THEN
  DELETE FROM invoyis.duplicate_candidates WHERE archive_id=target_archive AND (left_document_id=target_document OR right_document_id=target_document);
  DELETE FROM invoyis.retention_evaluations WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.retention_document_facts_history WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.retention_document_facts WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.retention_queue WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.document_holds WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.personal_reminders WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.preview_claims WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.preview_derivatives WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.extraction_runs WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.review_idempotency WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.document_trash_events WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.processing_jobs WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.inbound_parts WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.upload_receipts WHERE archive_id=target_archive AND document_id=target_document;
  UPDATE invoyis.documents SET current_confirmed_revision_id=NULL,latest_draft_revision_id=NULL
   WHERE archive_id=target_archive AND id=target_document;
  DELETE FROM invoyis.document_revisions WHERE archive_id=target_archive AND document_id=target_document;
  DELETE FROM invoyis.documents WHERE archive_id=target_archive AND id=target_document;
  DELETE FROM invoyis.submissions WHERE archive_id=target_archive AND id=ANY(submission_ids);
  DELETE FROM invoyis.archive_audit WHERE archive_id=target_archive AND details->>'documentId'=target_document::text;
 END IF;
 -- Only remove canonical metadata once every submission that needs it is gone.
 DELETE FROM invoyis.stored_objects o WHERE o.archive_id=target_archive
  AND NOT EXISTS(SELECT 1 FROM invoyis.submissions sub WHERE sub.stored_object_id=o.id)
  AND EXISTS(SELECT 1 FROM invoyis.shred_objects k WHERE k.archive_id=target_archive
   AND k.document_id=target_document AND k.stored_object_id=o.id AND k.status='verified');
 DELETE FROM invoyis.upload_sessions u WHERE u.archive_id=target_archive
  AND u.id=ANY(coalesce(session_ids,'{}'::uuid[]) || coalesce(canonical_session_ids,'{}'::uuid[]))
  AND NOT EXISTS(SELECT 1 FROM invoyis.submissions sub WHERE sub.upload_session_id=u.id)
  AND NOT EXISTS(SELECT 1 FROM invoyis.stored_objects o WHERE o.upload_session_id=u.id)
  AND NOT EXISTS(SELECT 1 FROM invoyis.upload_receipts r WHERE r.upload_session_id=u.id);
 UPDATE invoyis.shred_objects SET key_fingerprint=md5(storage_key),storage_key=NULL
  WHERE archive_id=target_archive AND document_id=target_document AND storage_key IS NOT NULL
   AND status IN ('verified','shared');
 UPDATE invoyis.document_shreds SET status='verified',verified_at=now(),last_error=NULL
  WHERE archive_id=target_archive AND document_id=target_document;
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION invoyis.finalize_document_shred(uuid,uuid) FROM PUBLIC;

CREATE OR REPLACE FUNCTION invoyis.authorize_document_shred(target_archive uuid,target_document uuid,
 expected_name text, expected_revision uuid, expected_decision uuid, expected_fingerprint text)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE docrow record; decision record; object_row record; session_row record;
DECLARE keep_date date; next_time timestamptz; referenced_object uuid; key_state text;
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
 IF invoyis.shred_export_blocked(target_archive,target_document) THEN RETURN 'active_export'; END IF;
 IF docrow.kind='business' THEN
  SELECT id,outcome,confirmed_revision_id,fingerprint INTO decision FROM invoyis.retention_evaluations
   WHERE archive_id=target_archive AND document_id=target_document ORDER BY created_at DESC,id DESC LIMIT 1;
  IF NOT FOUND OR decision.id IS DISTINCT FROM expected_decision
   OR decision.confirmed_revision_id IS DISTINCT FROM expected_revision
   OR decision.fingerprint IS DISTINCT FROM expected_fingerprint THEN RETURN 'retention_stale'; END IF;
  IF decision.outcome->>'status'<>'calculated' OR decision.outcome->>'keepThrough' IS NULL
  THEN RETURN 'retention_unresolved'; END IF;
  keep_date:=(decision.outcome->>'keepThrough')::date;
  IF keep_date >= (now() AT TIME ZONE 'UTC')::date THEN RETURN 'keep_through_date'; END IF;
 ELSIF docrow.kind='personal' THEN
  IF expected_decision IS NOT NULL THEN RETURN 'personal_reminder_not_authorization'; END IF;
 ELSE RETURN 'archive_kind'; END IF;
 IF NOT EXISTS(SELECT 1 FROM invoyis.upload_receipts WHERE archive_id=target_archive AND document_id=target_document)
 THEN RETURN 'receipt_missing'; END IF;
 SELECT o.id,o.storage_key,o.upload_session_id,u.grant_expires_at,u.finalise_by INTO object_row
 FROM invoyis.stored_objects o JOIN invoyis.upload_sessions u ON u.id=o.upload_session_id
 WHERE o.archive_id=target_archive AND o.id=docrow.stored_object_id FOR UPDATE OF o,u;
 IF NOT FOUND THEN RETURN 'original_missing'; END IF;
 UPDATE invoyis.documents SET shred_requested_at=now() WHERE archive_id=target_archive AND id=target_document;
 INSERT INTO invoyis.document_shreds(archive_id,document_id,actor_id,confirmed_revision_id,decision_id,keep_through,evidence)
 VALUES(target_archive,target_document,invoyis.current_account_id(),expected_revision,expected_decision,keep_date,
  jsonb_build_object('archiveKind',docrow.kind,'fingerprint',expected_fingerprint,'originalObjectId',object_row.id));
 DELETE FROM invoyis.preview_derivatives WHERE archive_id=target_archive AND document_id=target_document;
 key_state:=CASE WHEN EXISTS(SELECT 1 FROM invoyis.submissions s JOIN invoyis.documents d ON d.submission_id=s.id
  WHERE s.stored_object_id=object_row.id AND d.id<>target_document AND d.shred_requested_at IS NULL)
  THEN 'shared' ELSE 'pending' END;
 INSERT INTO invoyis.shred_objects(archive_id,document_id,stored_object_id,upload_session_id,storage_key,object_kind,status,next_attempt_at)
 VALUES(target_archive,target_document,object_row.id,object_row.upload_session_id,object_row.storage_key,'original',key_state,
  greatest(object_row.grant_expires_at,object_row.finalise_by,now()));
 FOR session_row IN SELECT u.* FROM invoyis.upload_sessions u JOIN invoyis.upload_receipts r ON r.upload_session_id=u.id
  WHERE r.archive_id=target_archive AND r.document_id=target_document ORDER BY u.id FOR UPDATE OF u LOOP
  next_time:=greatest(session_row.grant_expires_at,session_row.finalise_by,now());
  INSERT INTO invoyis.shred_objects(archive_id,document_id,upload_session_id,storage_key,object_kind,next_attempt_at)
   VALUES(target_archive,target_document,session_row.id,session_row.staging_key,'staging',next_time)
   ON CONFLICT DO NOTHING;
  INSERT INTO invoyis.shred_objects(archive_id,document_id,upload_session_id,storage_key,object_kind,next_attempt_at)
   VALUES(target_archive,target_document,session_row.id,session_row.prepared_key,'prepared',next_time)
   ON CONFLICT DO NOTHING;
  SELECT id INTO referenced_object FROM invoyis.stored_objects WHERE archive_id=target_archive AND storage_key=session_row.final_key;
  key_state:=CASE WHEN referenced_object IS NOT NULL AND EXISTS(
   SELECT 1 FROM invoyis.submissions s JOIN invoyis.documents d ON d.submission_id=s.id
   WHERE s.stored_object_id=referenced_object AND d.id<>target_document AND d.shred_requested_at IS NULL)
   THEN 'shared' ELSE 'pending' END;
  INSERT INTO invoyis.shred_objects(archive_id,document_id,stored_object_id,upload_session_id,storage_key,object_kind,status,next_attempt_at)
   VALUES(target_archive,target_document,referenced_object,session_row.id,session_row.final_key,'session_final',key_state,next_time)
   ON CONFLICT DO NOTHING;
  referenced_object:=NULL;
 END LOOP;
 PERFORM invoyis.finalize_document_shred(target_archive,target_document);
 RETURN 'pending';
END $$;

DROP FUNCTION invoyis.claim_shred_object();
CREATE FUNCTION invoyis.claim_shred_object()
RETURNS TABLE(ledger_id uuid,archive_id uuid,document_id uuid,storage_key text,token uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 RETURN QUERY WITH candidate AS (
  SELECT o.id FROM invoyis.shred_objects o WHERE o.status IN ('pending','failed')
   AND o.next_attempt_at<=now() AND (o.lease_until IS NULL OR o.lease_until<now())
  ORDER BY o.next_attempt_at,o.id LIMIT 1 FOR UPDATE SKIP LOCKED
 ), claimed AS (
  UPDATE invoyis.shred_objects o SET lease_token=gen_random_uuid(),lease_until=now()+interval '2 minutes',
   attempts=o.attempts+1,status='pending' FROM candidate c WHERE o.id=c.id
  RETURNING o.id,o.archive_id,o.document_id,o.storage_key,o.lease_token
 ) SELECT c.id,c.archive_id,c.document_id,c.storage_key,c.lease_token FROM claimed c;
 UPDATE invoyis.document_shreds s SET status='pending',last_error=NULL
 WHERE s.status='failed' AND EXISTS(SELECT 1 FROM invoyis.shred_objects o
  WHERE o.archive_id=s.archive_id AND o.document_id=s.document_id AND o.lease_until>now() AND o.lease_token IS NOT NULL);
END $$;
DROP FUNCTION invoyis.shred_object_safe(uuid,uuid,uuid,uuid);
CREATE FUNCTION invoyis.shred_object_safe(target_ledger uuid,owned_token uuid)
RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT EXISTS(SELECT 1 FROM invoyis.shred_objects k
  WHERE k.id=target_ledger AND k.lease_token=owned_token AND k.lease_until>now() AND k.status='pending'
   AND (k.upload_session_id IS NULL OR EXISTS(SELECT 1 FROM invoyis.upload_sessions u
    WHERE u.id=k.upload_session_id AND u.grant_expires_at<=now() AND u.finalise_by<=now()))
   AND NOT EXISTS(SELECT 1 FROM invoyis.stored_objects o JOIN invoyis.submissions s ON s.stored_object_id=o.id
    JOIN invoyis.documents d ON d.submission_id=s.id WHERE o.storage_key=k.storage_key
     AND d.id<>k.document_id AND d.shred_requested_at IS NULL))
$$;
DROP FUNCTION invoyis.finish_shred_object(uuid,uuid,uuid,uuid,boolean,text);
CREATE FUNCTION invoyis.finish_shred_object(target_ledger uuid,owned_token uuid,verified boolean,failure text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE changed integer; target_archive uuid; target_document uuid;
BEGIN
 UPDATE invoyis.shred_objects SET status=CASE WHEN verified THEN 'verified' ELSE 'failed' END,
  verified_at=CASE WHEN verified THEN now() ELSE NULL END,
  last_error=CASE WHEN verified THEN NULL ELSE left(failure,200) END,
  next_attempt_at=now()+make_interval(secs=>least(3600,power(2,least(attempts,11))::integer)),
  lease_token=NULL,lease_until=NULL
 WHERE id=target_ledger AND lease_token=owned_token AND lease_until>now()
 RETURNING archive_id,document_id INTO target_archive,target_document;
 GET DIAGNOSTICS changed=ROW_COUNT;
 IF changed<>1 THEN RETURN false; END IF;
 IF verified THEN PERFORM invoyis.finalize_document_shred(target_archive,target_document);
 ELSE UPDATE invoyis.document_shreds SET status='failed',last_error=left(failure,200)
  WHERE archive_id=target_archive AND document_id=target_document; END IF;
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION invoyis.claim_shred_object(),invoyis.shred_object_safe(uuid,uuid),
 invoyis.finish_shred_object(uuid,uuid,boolean,text) FROM PUBLIC;
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
 GRANT EXECUTE ON FUNCTION invoyis.finalize_document_shred(uuid,uuid),invoyis.claim_shred_object(),
  invoyis.shred_object_safe(uuid,uuid),invoyis.finish_shred_object(uuid,uuid,boolean,text) TO invoyis_archive_runtime;
END IF; END $$;
