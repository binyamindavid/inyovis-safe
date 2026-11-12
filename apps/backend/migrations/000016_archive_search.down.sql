DO $$
DECLARE
 created_here boolean;
BEGIN
 SELECT obj_description('invoyis.archive_search_unaccent(text)'::regprocedure, 'pg_proc') = 'migration-16-owned-unaccent' INTO created_here;
 DROP FUNCTION invoyis.archive_search_unaccent(text);
 IF created_here THEN
  DROP EXTENSION unaccent;
 END IF;
END $$;
