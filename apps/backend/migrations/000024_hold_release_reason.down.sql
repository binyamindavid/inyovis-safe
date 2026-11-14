-- A released hold's reason is audit evidence. Use a forward repair once one exists.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.document_holds WHERE released_at IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot roll back release reasons: released hold evidence exists';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION invoyis.hold_release_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.id IS DISTINCT FROM OLD.id OR NEW.archive_id IS DISTINCT FROM OLD.archive_id OR NEW.document_id IS DISTINCT FROM OLD.document_id OR NEW.reason IS DISTINCT FROM OLD.reason OR NEW.placed_by IS DISTINCT FROM OLD.placed_by OR NEW.placed_at IS DISTINCT FROM OLD.placed_at OR OLD.released_at IS NOT NULL OR NEW.released_at IS NULL OR NEW.released_by IS NULL THEN RAISE EXCEPTION 'hold history is immutable'; END IF;
 RETURN NEW;
END $$;
ALTER TABLE invoyis.document_holds DROP CONSTRAINT document_holds_release_reason_check;
ALTER TABLE invoyis.document_holds DROP COLUMN release_reason;
