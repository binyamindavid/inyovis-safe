CREATE TABLE invoyis.archive_exports (
 id uuid PRIMARY KEY,
 archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 storage_key text NOT NULL UNIQUE,
 state text NOT NULL CHECK (state IN ('building','ready','failed')),
 items jsonb NOT NULL DEFAULT '[]'::jsonb,
 bundle_sha256 text,
 byte_count bigint,
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL,
 CHECK (state <> 'ready' OR (bundle_sha256 ~ '^[a-f0-9]{64}$' AND byte_count > 0))
);
CREATE INDEX archive_exports_cleanup ON invoyis.archive_exports(expires_at,created_at)
 WHERE state <> 'ready';
CREATE INDEX archive_exports_expiry ON invoyis.archive_exports(expires_at);
ALTER TABLE invoyis.archive_exports ENABLE ROW LEVEL SECURITY;
CREATE POLICY archive_exports_member ON invoyis.archive_exports FOR ALL
 USING (invoyis.in_requested_archive(archive_id) AND EXISTS (
   SELECT 1 FROM invoyis.archive_memberships m WHERE m.archive_id=archive_exports.archive_id
   AND m.account_id=invoyis.current_account_id() AND m.can_bulk_export))
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND EXISTS (
   SELECT 1 FROM invoyis.archive_memberships m WHERE m.archive_id=archive_exports.archive_id
   AND m.account_id=invoyis.current_account_id() AND m.can_bulk_export));

-- Cleanup only exposes expired bundles or abandoned/failed builds to the API runtime.
CREATE FUNCTION invoyis.export_cleanup_candidates(batch_size integer)
RETURNS TABLE(id uuid, storage_key text) LANGUAGE sql SECURITY DEFINER
SET search_path=pg_catalog,invoyis AS $$
 SELECT e.id,e.storage_key FROM invoyis.archive_exports e
 WHERE e.expires_at <= now() OR (e.state <> 'ready' AND e.created_at < now()-interval '5 minutes')
 ORDER BY e.created_at,e.id LIMIT least(greatest(batch_size,0),20)
$$;
CREATE FUNCTION invoyis.export_cleanup_done(target uuid) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 WITH removed AS (DELETE FROM invoyis.archive_exports e WHERE e.id=target
 AND (e.expires_at <= now() OR (e.state <> 'ready' AND e.created_at < now()-interval '5 minutes')) RETURNING 1)
 SELECT EXISTS(SELECT 1 FROM removed)
$$;
REVOKE ALL ON FUNCTION invoyis.export_cleanup_candidates(integer),invoyis.export_cleanup_done(uuid) FROM PUBLIC;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT,INSERT,UPDATE,DELETE ON invoyis.archive_exports TO invoyis_archive_runtime;
  GRANT EXECUTE ON FUNCTION invoyis.export_cleanup_candidates(integer),invoyis.export_cleanup_done(uuid) TO invoyis_archive_runtime;
 END IF;
END $$;
