ALTER TABLE invoyis.archive_audit ADD COLUMN system_actor text;
CREATE FUNCTION invoyis.can_submit(target uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, invoyis AS $$
 SELECT EXISTS (SELECT 1 FROM invoyis.archive_memberships m WHERE m.archive_id=target
 AND m.account_id=invoyis.current_account_id() AND m.role IN ('admin','contributor'))
$$;
CREATE TABLE invoyis.upload_sessions (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 uploader_id bigint NOT NULL REFERENCES invoyis.accounts(id), idempotency_key text NOT NULL,
 filename text NOT NULL, media_type text NOT NULL CHECK (media_type='application/pdf'),
 declared_bytes bigint NOT NULL CHECK (declared_bytes>0), declared_sha256 text NOT NULL CHECK (declared_sha256 ~ '^[a-f0-9]{64}$'),
 staging_key text NOT NULL UNIQUE, prepared_key text NOT NULL UNIQUE, final_key text NOT NULL UNIQUE,
 state text NOT NULL DEFAULT 'created' CHECK (state IN ('created','staged','finalising','complete','expired','failed')),
 grant_expires_at timestamptz NOT NULL, finalise_by timestamptz NOT NULL,
 verified_bytes bigint, verified_sha256 text, finalising_at timestamptz,
 reconcile_attempts integer NOT NULL DEFAULT 0, next_reconcile_at timestamptz NOT NULL DEFAULT now(),
 orphan_reason text, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (archive_id,uploader_id,idempotency_key), UNIQUE (archive_id,id)
);
CREATE INDEX upload_sessions_reconcile ON invoyis.upload_sessions(finalising_at,id) WHERE state='finalising';
CREATE TABLE invoyis.stored_objects (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 upload_session_id uuid NOT NULL UNIQUE REFERENCES invoyis.upload_sessions(id),
 storage_key text NOT NULL UNIQUE, byte_count bigint NOT NULL, sha256 text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE invoyis.submissions (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 upload_session_id uuid NOT NULL UNIQUE REFERENCES invoyis.upload_sessions(id),
 stored_object_id uuid NOT NULL UNIQUE REFERENCES invoyis.stored_objects(id),
 uploader_id bigint NOT NULL REFERENCES invoyis.accounts(id), source text NOT NULL CHECK (source='browser_upload'),
 original_filename text NOT NULL, received_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE invoyis.documents (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 submission_id uuid NOT NULL UNIQUE REFERENCES invoyis.submissions(id),
 state text NOT NULL CHECK (state='received'), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE invoyis.processing_jobs (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 document_id uuid NOT NULL UNIQUE REFERENCES invoyis.documents(id),
 state text NOT NULL CHECK (state='pending'), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE invoyis.upload_receipts (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 upload_session_id uuid NOT NULL UNIQUE REFERENCES invoyis.upload_sessions(id),
 submission_id uuid NOT NULL UNIQUE REFERENCES invoyis.submissions(id),
 document_id uuid NOT NULL UNIQUE REFERENCES invoyis.documents(id),
 byte_count bigint NOT NULL, sha256 text NOT NULL, received_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE invoyis.upload_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.stored_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.submissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.processing_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.upload_receipts ENABLE ROW LEVEL SECURITY;
CREATE POLICY upload_session_access ON invoyis.upload_sessions FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND uploader_id=invoyis.current_account_id() AND invoyis.can_submit(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND uploader_id=invoyis.current_account_id() AND invoyis.can_submit(archive_id));
CREATE POLICY stored_object_access ON invoyis.stored_objects FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
CREATE POLICY submission_access ON invoyis.submissions FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND uploader_id=invoyis.current_account_id() AND invoyis.can_submit(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND uploader_id=invoyis.current_account_id() AND invoyis.can_submit(archive_id));
CREATE POLICY document_access ON invoyis.documents FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
CREATE POLICY job_access ON invoyis.processing_jobs FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
CREATE POLICY receipt_access ON invoyis.upload_receipts FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
-- Reconciler discovery reveals identifiers only and requires its own runtime grant.
CREATE FUNCTION invoyis.stalled_upload_ids(batch_size integer)
RETURNS TABLE(archive_id uuid, session_id uuid) LANGUAGE sql SECURITY DEFINER
SET search_path=pg_catalog,invoyis AS $$
 SELECT archive_id,id FROM invoyis.upload_sessions WHERE state='finalising' AND orphan_reason IS NULL
 AND finalising_at < now()-interval '30 seconds' AND next_reconcile_at<=now()
 ORDER BY finalising_at,id LIMIT least(greatest(batch_size,0),20)
$$;
REVOKE ALL ON FUNCTION invoyis.stalled_upload_ids(integer) FROM PUBLIC;
CREATE FUNCTION invoyis.upload_uploader(target_archive uuid, target_session uuid) RETURNS bigint
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT uploader_id FROM invoyis.upload_sessions WHERE archive_id=target_archive AND id=target_session AND state='finalising'
$$;
REVOKE ALL ON FUNCTION invoyis.upload_uploader(uuid,uuid) FROM PUBLIC;
CREATE FUNCTION invoyis.expire_abandoned_uploads() RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE changed integer;
BEGIN
 UPDATE invoyis.upload_sessions SET state='expired' WHERE state IN ('created','staged') AND finalise_by<now();
 GET DIAGNOSTICS changed=ROW_COUNT;
 RETURN changed;
END $$;
CREATE FUNCTION invoyis.flag_upload_orphan(target_archive uuid,target_session uuid) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 WITH flagged AS (UPDATE invoyis.upload_sessions SET orphan_reason='receipt_blocked'
 WHERE archive_id=target_archive AND id=target_session AND state='finalising' AND orphan_reason IS NULL RETURNING 1)
 SELECT EXISTS(SELECT 1 FROM flagged)
$$;
CREATE FUNCTION invoyis.defer_upload_reconcile(target_archive uuid,target_session uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 UPDATE invoyis.upload_sessions SET reconcile_attempts=reconcile_attempts+1,
 next_reconcile_at=now()+make_interval(secs=>least(300,power(2,least(reconcile_attempts,8))::integer))
 WHERE archive_id=target_archive AND id=target_session AND state='finalising' AND orphan_reason IS NULL
$$;
REVOKE ALL ON FUNCTION invoyis.expire_abandoned_uploads() FROM PUBLIC;
REVOKE ALL ON FUNCTION invoyis.flag_upload_orphan(uuid,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION invoyis.defer_upload_reconcile(uuid,uuid) FROM PUBLIC;
REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA invoyis FROM PUBLIC;
ALTER DEFAULT PRIVILEGES IN SCHEMA invoyis REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
