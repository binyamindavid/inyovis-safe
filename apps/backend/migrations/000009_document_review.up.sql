ALTER TABLE invoyis.documents
  ADD COLUMN review_version bigint NOT NULL DEFAULT 0 CHECK (review_version >= 0),
  ADD COLUMN current_confirmed_revision_id uuid,
  ADD COLUMN latest_draft_revision_id uuid,
  ADD CONSTRAINT documents_archive_id_unique UNIQUE (archive_id,id);
ALTER TABLE invoyis.extraction_runs ADD CONSTRAINT extraction_runs_document_identity UNIQUE (archive_id,document_id,id);

CREATE TABLE invoyis.document_revisions (
 id uuid PRIMARY KEY,
 archive_id uuid NOT NULL,
 document_id uuid NOT NULL,
 actor_id bigint NOT NULL REFERENCES invoyis.accounts(id),
 kind text NOT NULL CHECK (kind IN ('draft','confirmed')),
 preceding_revision_id uuid,
 source_extraction_run_id uuid,
 metadata jsonb NOT NULL,
 issue_date date,
 subtotal numeric(24,8),
 stated_tax numeric(24,8),
 total numeric(24,8),
 provenance jsonb NOT NULL,
 acknowledged_warnings jsonb NOT NULL DEFAULT '[]'::jsonb,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (archive_id,document_id,id),
 FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.documents(archive_id,id),
 FOREIGN KEY (archive_id,document_id,preceding_revision_id) REFERENCES invoyis.document_revisions(archive_id,document_id,id),
 FOREIGN KEY (archive_id,document_id,source_extraction_run_id) REFERENCES invoyis.extraction_runs(archive_id,document_id,id)
);
ALTER TABLE invoyis.documents ADD CONSTRAINT confirmed_revision_same_document
 FOREIGN KEY (archive_id,id,current_confirmed_revision_id)
 REFERENCES invoyis.document_revisions(archive_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE invoyis.documents ADD CONSTRAINT draft_revision_same_document
 FOREIGN KEY (archive_id,id,latest_draft_revision_id)
 REFERENCES invoyis.document_revisions(archive_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX document_revisions_history ON invoyis.document_revisions(archive_id,document_id,created_at DESC,id DESC);
CREATE TABLE invoyis.review_idempotency (
 archive_id uuid NOT NULL,
 document_id uuid NOT NULL,
 actor_id bigint NOT NULL REFERENCES invoyis.accounts(id),
 idempotency_key uuid NOT NULL,
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (actor_id,document_id,idempotency_key),
 FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.documents(archive_id,id)
);
ALTER TABLE invoyis.document_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.review_idempotency ENABLE ROW LEVEL SECURITY;
CREATE POLICY revision_read ON invoyis.document_revisions FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY revision_insert ON invoyis.document_revisions FOR INSERT
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id) AND actor_id=invoyis.current_account_id());
CREATE POLICY idempotency_access ON invoyis.review_idempotency FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id) AND actor_id=invoyis.current_account_id())
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id) AND actor_id=invoyis.current_account_id());

DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT,INSERT ON invoyis.document_revisions,invoyis.review_idempotency TO invoyis_archive_runtime;
  GRANT UPDATE (review_version,current_confirmed_revision_id,latest_draft_revision_id,state) ON invoyis.documents TO invoyis_archive_runtime;
 END IF;
END $$;

-- A later processing run restores the human outcome without overwriting revisions.
CREATE OR REPLACE FUNCTION invoyis.worker_complete(target uuid, owned_token uuid, input_digest text,
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
 UPDATE invoyis.documents SET state=CASE WHEN current_confirmed_revision_id IS NULL THEN 'needs_review' ELSE 'ready' END,clearance='cleared' WHERE id=doc;
 RETURN true;
END $$;

