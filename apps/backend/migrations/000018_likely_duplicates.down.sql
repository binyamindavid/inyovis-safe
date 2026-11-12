DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.duplicate_candidates WHERE resolved_at IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove audited duplicate decisions';
 END IF;
END $$;
DROP TABLE invoyis.duplicate_candidates;
