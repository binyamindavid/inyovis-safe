CREATE OR REPLACE FUNCTION invoyis.retention_coverage_publication_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.version IS DISTINCT FROM OLD.version THEN
  PERFORM invoyis.validate_retention_coverage(NEW.version);
 END IF;
 RETURN NEW;
END $$;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.retention_rule_versions
  WHERE validation_record->>'status' IS DISTINCT FROM 'synthetic' OR exceptions <> '[]'::jsonb)
 THEN RAISE EXCEPTION 'migration 25 contains catalogue validation evidence; use a forward repair'; END IF;
END $$;
DROP TRIGGER retention_personal_set_changed ON invoyis.retention_active_rule_set;
DROP FUNCTION invoyis.retention_personal_set_changed();
DROP FUNCTION invoyis.validate_retention_catalogue(text);
ALTER TABLE invoyis.retention_rule_versions DROP COLUMN validation_record, DROP COLUMN exceptions;
