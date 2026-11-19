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
  jsonb_build_object('archiveKind',docrow.kind,'fingerprint',expected_fingerprint,'originalObjectId',object_row.id,'fromTrash',docrow.trashed_at IS NOT NULL));
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
