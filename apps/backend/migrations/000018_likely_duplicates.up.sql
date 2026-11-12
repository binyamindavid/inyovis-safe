CREATE TABLE invoyis.duplicate_candidates (
 id uuid PRIMARY KEY,
 archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 left_document_id uuid NOT NULL,
 right_document_id uuid NOT NULL,
 left_revision_id uuid NOT NULL,
 right_revision_id uuid NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','duplicate','keep_both','stale')),
 resolved_by bigint REFERENCES invoyis.accounts(id),
 resolved_at timestamptz,
 resolved_left_revision_id uuid,
 resolved_right_revision_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT duplicate_candidates_order CHECK (left_document_id<right_document_id),
 CONSTRAINT duplicate_candidates_resolution CHECK (
  (resolved_by IS NULL AND resolved_at IS NULL AND resolved_left_revision_id IS NULL AND resolved_right_revision_id IS NULL)
  OR (resolved_by IS NOT NULL AND resolved_at IS NOT NULL AND resolved_left_revision_id IS NOT NULL AND resolved_right_revision_id IS NOT NULL)),
 UNIQUE (archive_id,left_document_id,right_document_id),
 FOREIGN KEY (archive_id,left_document_id) REFERENCES invoyis.documents(archive_id,id),
 FOREIGN KEY (archive_id,right_document_id) REFERENCES invoyis.documents(archive_id,id),
 FOREIGN KEY (archive_id,left_document_id,left_revision_id) REFERENCES invoyis.document_revisions(archive_id,document_id,id),
 FOREIGN KEY (archive_id,right_document_id,right_revision_id) REFERENCES invoyis.document_revisions(archive_id,document_id,id),
 FOREIGN KEY (archive_id,left_document_id,resolved_left_revision_id) REFERENCES invoyis.document_revisions(archive_id,document_id,id),
 FOREIGN KEY (archive_id,right_document_id,resolved_right_revision_id) REFERENCES invoyis.document_revisions(archive_id,document_id,id)
);
CREATE INDEX duplicate_candidates_queue ON invoyis.duplicate_candidates(archive_id,created_at,id) WHERE state='pending';
-- Backfill current confirmations; drafts and exact duplicate originals do not qualify.
INSERT INTO invoyis.duplicate_candidates(id,archive_id,left_document_id,right_document_id,left_revision_id,right_revision_id)
SELECT gen_random_uuid(),a.archive_id,a.id,b.id,ra.id,rb.id
FROM invoyis.documents a JOIN invoyis.documents b ON b.archive_id=a.archive_id AND a.id<b.id
JOIN invoyis.document_revisions ra ON ra.id=a.current_confirmed_revision_id AND ra.archive_id=a.archive_id
JOIN invoyis.document_revisions rb ON rb.id=b.current_confirmed_revision_id AND rb.archive_id=b.archive_id
JOIN invoyis.submissions sa ON sa.id=a.submission_id AND sa.archive_id=a.archive_id
JOIN invoyis.submissions sb ON sb.id=b.submission_id AND sb.archive_id=b.archive_id
JOIN invoyis.stored_objects oa ON oa.id=sa.stored_object_id AND oa.archive_id=a.archive_id
JOIN invoyis.stored_objects ob ON ob.id=sb.stored_object_id AND ob.archive_id=b.archive_id
WHERE a.state='ready' AND b.state='ready' AND a.clearance='cleared' AND b.clearance='cleared'
 AND oa.id<>ob.id AND oa.sha256<>ob.sha256 AND ra.kind='confirmed' AND rb.kind='confirmed'
 AND ra.supplier_id IS NOT NULL AND ra.supplier_id=rb.supplier_id
 AND nullif(ra.metadata->>'invoiceId','') IS NOT NULL AND ra.metadata->>'invoiceId'=rb.metadata->>'invoiceId'
 AND ra.issue_date IS NOT NULL AND ra.issue_date=rb.issue_date
 AND ra.total IS NOT NULL AND ra.total=rb.total
 AND nullif(ra.metadata->>'currency','') IS NOT NULL AND ra.metadata->>'currency'=rb.metadata->>'currency';
ALTER TABLE invoyis.duplicate_candidates ENABLE ROW LEVEL SECURITY;
CREATE POLICY duplicate_candidates_read ON invoyis.duplicate_candidates FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY duplicate_candidates_insert ON invoyis.duplicate_candidates FOR INSERT
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
CREATE POLICY duplicate_candidates_update ON invoyis.duplicate_candidates FOR UPDATE
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT,INSERT,UPDATE ON invoyis.duplicate_candidates TO invoyis_archive_runtime;
 END IF;
END $$;
