CREATE TABLE invoyis.preview_derivatives (
 archive_id uuid NOT NULL,
 document_id uuid NOT NULL,
 input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[a-f0-9]{64}$'),
 page integer NOT NULL CHECK (page BETWEEN 1 AND 10),
 page_count integer NOT NULL CHECK (page_count BETWEEN 1 AND 10000),
 renderer_version text NOT NULL,
 image bytea NOT NULL CHECK (octet_length(image) <= 4000000),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (document_id,input_sha256,page,renderer_version),
 FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.documents(archive_id,id)
);
CREATE TABLE invoyis.preview_claims (
 archive_id uuid NOT NULL,
 document_id uuid NOT NULL,
 input_sha256 text NOT NULL,
 page integer NOT NULL,
 renderer_version text NOT NULL,
 owner uuid NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY (document_id,input_sha256,page,renderer_version),
 FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.documents(archive_id,id)
);
ALTER TABLE invoyis.preview_derivatives ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.preview_claims ENABLE ROW LEVEL SECURITY;
CREATE POLICY preview_derivative_access ON invoyis.preview_derivatives FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY preview_claim_access ON invoyis.preview_claims FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY stored_object_member_preview ON invoyis.stored_objects FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE TABLE invoyis.preview_rate (
 archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 window_start timestamptz NOT NULL,
 requests integer NOT NULL CHECK (requests BETWEEN 1 AND 60),
 PRIMARY KEY (archive_id,window_start)
);
ALTER TABLE invoyis.preview_rate ENABLE ROW LEVEL SECURITY;
CREATE POLICY preview_rate_access ON invoyis.preview_rate FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT,INSERT,UPDATE,DELETE ON invoyis.preview_derivatives,invoyis.preview_claims,invoyis.preview_rate TO invoyis_archive_runtime;
 END IF;
END $$;
