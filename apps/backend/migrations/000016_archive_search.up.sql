-- Search transliterates accents at comparison time; stored printed text stays unchanged.
-- Keep an existing installation in its schema. The wrapper records ownership so
-- rollback removes only an extension installed by this migration.
DO $$
DECLARE
 extension_schema text;
 created_here boolean;
BEGIN
 SELECT NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname='unaccent') INTO created_here;
 IF created_here THEN
  CREATE EXTENSION unaccent WITH SCHEMA invoyis;
 END IF;
 SELECT n.nspname INTO extension_schema
 FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace
 WHERE e.extname='unaccent';
 EXECUTE format(
  'CREATE FUNCTION invoyis.archive_search_unaccent(text) RETURNS text LANGUAGE SQL STABLE STRICT PARALLEL SAFE AS %L',
  format('SELECT %I.unaccent($1)', extension_schema)
 );
 IF created_here THEN
  COMMENT ON FUNCTION invoyis.archive_search_unaccent(text) IS 'migration-16-owned-unaccent';
 END IF;
END $$;
