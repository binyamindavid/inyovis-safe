ALTER TABLE invoyis.processing_jobs DROP CONSTRAINT processing_jobs_state_check;
ALTER TABLE invoyis.processing_jobs ADD CONSTRAINT processing_jobs_state_check CHECK (state IN ('pending','running','succeeded','failed'));
ALTER TABLE invoyis.processing_jobs
 ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 ADD COLUMN parser_attempts integer NOT NULL DEFAULT 0 CHECK (parser_attempts >= 0),
 ADD COLUMN next_run_at timestamptz NOT NULL DEFAULT now(),
 ADD COLUMN lease_token uuid,
 ADD COLUMN lease_expires_at timestamptz,
 ADD COLUMN failure_code text CHECK (failure_code IS NULL OR failure_code IN (
  'storage_unavailable','scanner_unavailable','original_integrity_mismatch','scan_rejected',
  'parser_timeout','parser_resource_exhausted','invalid_pdf','unsafe_xml','content_limit_exceeded',
  'lease_expired_exhausted','parser_attempts_exhausted')),
 ADD CONSTRAINT processing_jobs_lease_pair CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
 ADD CONSTRAINT processing_jobs_running_lease CHECK ((state='running') = (lease_token IS NOT NULL));
CREATE INDEX processing_jobs_due ON invoyis.processing_jobs(next_run_at,id) WHERE state='pending';
CREATE INDEX processing_jobs_expired ON invoyis.processing_jobs(lease_expires_at,id) WHERE state='running';

ALTER TABLE invoyis.documents DROP CONSTRAINT documents_state_check;
ALTER TABLE invoyis.documents ADD CONSTRAINT documents_state_check CHECK (state IN ('received','quarantined','extracting','needs_review','ready','failed'));
ALTER TABLE invoyis.documents ADD COLUMN clearance text NOT NULL DEFAULT 'pending' CHECK (clearance IN ('pending','cleared','blocked'));
CREATE POLICY document_member_read ON invoyis.documents FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY job_member_read ON invoyis.processing_jobs FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY submission_member_read ON invoyis.submissions FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));

CREATE TABLE invoyis.extraction_runs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 document_id uuid NOT NULL REFERENCES invoyis.documents(id),
 stage text NOT NULL CHECK (stage='document_xml'),
 input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[a-f0-9]{64}$'),
 extractor_version text NOT NULL,
 profile text,
 candidates jsonb NOT NULL DEFAULT '{}'::jsonb,
 provenance jsonb NOT NULL DEFAULT '{}'::jsonb,
 warnings jsonb NOT NULL DEFAULT '[]'::jsonb,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(document_id,stage,input_sha256,extractor_version)
);
ALTER TABLE invoyis.extraction_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY extraction_run_access ON invoyis.extraction_runs FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));

-- All worker functions run as the migration owner. The worker role receives no
-- table grants; each mutation derives document/archive identity from its job.
CREATE FUNCTION invoyis.worker_reap(max_claims integer,max_parser_starts integer) RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE changed integer;
BEGIN
 WITH expired AS (
  SELECT id,document_id,attempts,parser_attempts FROM invoyis.processing_jobs
  WHERE state='running' AND lease_expires_at <= now() FOR UPDATE SKIP LOCKED
 ), updated AS (
  UPDATE invoyis.processing_jobs j SET
   state=CASE WHEN e.attempts >= max_claims OR e.parser_attempts >= max_parser_starts THEN 'failed' ELSE 'pending' END,
   failure_code=CASE WHEN e.attempts >= max_claims THEN 'lease_expired_exhausted'
    WHEN e.parser_attempts >= max_parser_starts THEN 'parser_attempts_exhausted' ELSE NULL END,
   lease_token=NULL, lease_expires_at=NULL, next_run_at=now()
  FROM expired e WHERE j.id=e.id RETURNING j.document_id,j.state
 ), failed AS (
  UPDATE invoyis.documents d SET state='failed',clearance='blocked'
  FROM updated u WHERE d.id=u.document_id AND u.state='failed' RETURNING d.id
 )
 UPDATE invoyis.documents d SET state='quarantined',clearance='pending'
 FROM updated u WHERE d.id=u.document_id AND u.state='pending';
 GET DIAGNOSTICS changed=ROW_COUNT;
 RETURN changed;
END $$;

CREATE FUNCTION invoyis.worker_claim(max_claims integer, lease_seconds integer)
RETURNS TABLE(job_id uuid, archive_id uuid, document_id uuid, storage_key text, byte_count bigint, sha256 text, token uuid, claims integer, parser_starts integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 RETURN QUERY
 WITH next_job AS (
  SELECT j.id FROM invoyis.processing_jobs j
  WHERE j.state='pending' AND j.next_run_at<=now() AND j.attempts<max_claims
  ORDER BY j.next_run_at,j.id LIMIT 1 FOR UPDATE SKIP LOCKED
 ), claimed AS (
  UPDATE invoyis.processing_jobs j SET state='running',attempts=j.attempts+1,
   lease_token=gen_random_uuid(),lease_expires_at=now()+make_interval(secs=>lease_seconds),failure_code=NULL
  FROM next_job n WHERE j.id=n.id
  RETURNING j.id,j.archive_id,j.document_id,j.lease_token,j.attempts,j.parser_attempts
 ), moved AS (
  UPDATE invoyis.documents d SET state='quarantined',clearance='pending'
  FROM claimed c WHERE d.id=c.document_id AND d.state='received' RETURNING d.id
 )
 SELECT c.id,c.archive_id,c.document_id,o.storage_key,o.byte_count,o.sha256,c.lease_token,c.attempts,c.parser_attempts
 FROM claimed c JOIN invoyis.documents d ON d.id=c.document_id
 JOIN invoyis.submissions s ON s.id=d.submission_id
 JOIN invoyis.stored_objects o ON o.id=s.stored_object_id;
END $$;

CREATE FUNCTION invoyis.worker_heartbeat(target uuid, owned_token uuid, lease_seconds integer) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 WITH updated AS (UPDATE invoyis.processing_jobs SET lease_expires_at=now()+make_interval(secs=>lease_seconds)
 WHERE id=target AND state='running' AND lease_token=owned_token AND lease_expires_at>now() RETURNING 1)
 SELECT EXISTS(SELECT 1 FROM updated)
$$;

CREATE FUNCTION invoyis.worker_start_parser(target uuid, owned_token uuid, max_parser_starts integer)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE doc uuid;
BEGIN
 UPDATE invoyis.processing_jobs SET parser_attempts=parser_attempts+1
 WHERE id=target AND state='running' AND lease_token=owned_token AND lease_expires_at>now()
 AND parser_attempts<max_parser_starts RETURNING document_id INTO doc;
 IF doc IS NULL THEN RETURN false; END IF;
 UPDATE invoyis.documents SET state='extracting' WHERE id=doc;
 RETURN true;
END $$;

CREATE FUNCTION invoyis.worker_retry(target uuid, owned_token uuid, code text, delay_seconds integer, max_claims integer)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE doc uuid; terminal boolean;
BEGIN
 UPDATE invoyis.processing_jobs SET state=CASE WHEN attempts>=max_claims THEN 'failed' ELSE 'pending' END,
  failure_code=CASE WHEN attempts>=max_claims THEN code ELSE NULL END,
  next_run_at=now()+make_interval(secs=>delay_seconds),lease_token=NULL,lease_expires_at=NULL
 WHERE id=target AND state='running' AND lease_token=owned_token AND lease_expires_at>now()
 RETURNING document_id,state='failed' INTO doc,terminal;
 IF doc IS NULL THEN RETURN false; END IF;
 UPDATE invoyis.documents SET state=CASE WHEN terminal THEN 'failed' ELSE 'quarantined' END,
  clearance=CASE WHEN terminal THEN 'blocked' ELSE 'pending' END WHERE id=doc;
 RETURN true;
END $$;

CREATE FUNCTION invoyis.worker_fail(target uuid, owned_token uuid, code text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE doc uuid;
BEGIN
 UPDATE invoyis.processing_jobs SET state='failed',failure_code=code,lease_token=NULL,lease_expires_at=NULL
 WHERE id=target AND state='running' AND lease_token=owned_token AND lease_expires_at>now()
 RETURNING document_id INTO doc;
 IF doc IS NULL THEN RETURN false; END IF;
 UPDATE invoyis.documents SET state='failed',clearance='blocked' WHERE id=doc;
 RETURN true;
END $$;

CREATE FUNCTION invoyis.worker_complete(target uuid, owned_token uuid, input_digest text,
 version text, detected_profile text, fields jsonb, sources jsonb, notices jsonb)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE doc uuid; archive uuid; recorded_digest text;
BEGIN
 SELECT j.document_id,j.archive_id,o.sha256 INTO doc,archive,recorded_digest
 FROM invoyis.processing_jobs j JOIN invoyis.documents d ON d.id=j.document_id
 JOIN invoyis.submissions s ON s.id=d.submission_id JOIN invoyis.stored_objects o ON o.id=s.stored_object_id
 WHERE j.id=target AND j.state='running' AND j.lease_token=owned_token AND j.lease_expires_at>now()
 FOR UPDATE OF j;
 IF doc IS NULL THEN RETURN false; END IF;
 IF input_digest<>recorded_digest THEN RAISE EXCEPTION 'input digest differs from stored original'; END IF;
 INSERT INTO invoyis.extraction_runs(archive_id,document_id,stage,input_sha256,extractor_version,profile,candidates,provenance,warnings)
 VALUES(archive,doc,'document_xml',input_digest,version,detected_profile,fields,sources,notices)
 ON CONFLICT(document_id,stage,input_sha256,extractor_version) DO NOTHING;
 UPDATE invoyis.processing_jobs SET state='succeeded',failure_code=NULL,lease_token=NULL,lease_expires_at=NULL WHERE id=target;
 UPDATE invoyis.documents SET state='needs_review',clearance='cleared' WHERE id=doc;
 RETURN true;
END $$;

CREATE FUNCTION invoyis.worker_metrics()
RETURNS TABLE(oldest_eligible_seconds bigint, terminal_code text, terminal_count bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT (SELECT EXTRACT(EPOCH FROM now()-min(created_at))::bigint FROM invoyis.processing_jobs
         WHERE state='pending' AND next_run_at<=now()),failure_code,count(*)
 FROM invoyis.processing_jobs WHERE state='failed' GROUP BY failure_code
 UNION ALL SELECT (SELECT EXTRACT(EPOCH FROM now()-min(created_at))::bigint FROM invoyis.processing_jobs
         WHERE state='pending' AND next_run_at<=now()),NULL,0
 WHERE NOT EXISTS(SELECT 1 FROM invoyis.processing_jobs WHERE state='failed')
$$;

REVOKE EXECUTE ON FUNCTION invoyis.worker_reap(integer,integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION invoyis.worker_claim(integer,integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION invoyis.worker_heartbeat(uuid,uuid,integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION invoyis.worker_start_parser(uuid,uuid,integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION invoyis.worker_retry(uuid,uuid,text,integer,integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION invoyis.worker_fail(uuid,uuid,text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION invoyis.worker_complete(uuid,uuid,text,text,text,jsonb,jsonb,jsonb) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION invoyis.worker_metrics() FROM PUBLIC;
