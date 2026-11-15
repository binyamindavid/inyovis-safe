CREATE TABLE invoyis.forwarding_addresses (
 token text PRIMARY KEY CHECK (token ~ '^[a-z2-7]{16}$'),
 archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 state text NOT NULL CHECK (state IN ('active','disabled','retired')),
 created_at timestamptz NOT NULL DEFAULT now(),
 retired_at timestamptz,
 UNIQUE (archive_id, token)
);
CREATE UNIQUE INDEX forwarding_one_current ON invoyis.forwarding_addresses(archive_id)
 WHERE state <> 'retired';
CREATE INDEX forwarding_archive_history ON invoyis.forwarding_addresses(archive_id,created_at DESC);
ALTER TABLE invoyis.forwarding_addresses ENABLE ROW LEVEL SECURITY;
CREATE POLICY forwarding_admin ON invoyis.forwarding_addresses FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT,INSERT,UPDATE ON invoyis.forwarding_addresses TO invoyis_archive_runtime;
 END IF;
END $$;
