ALTER TABLE invoyis.document_holds ADD COLUMN release_reason text;
ALTER TABLE invoyis.document_holds ADD CONSTRAINT document_holds_release_reason_check
 CHECK ((released_at IS NOT NULL OR release_reason IS NULL) AND
        (release_reason IS NULL OR length(btrim(release_reason)) BETWEEN 1 AND 1000));
CREATE OR REPLACE FUNCTION invoyis.hold_release_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.id IS DISTINCT FROM OLD.id OR NEW.archive_id IS DISTINCT FROM OLD.archive_id OR NEW.document_id IS DISTINCT FROM OLD.document_id OR NEW.reason IS DISTINCT FROM OLD.reason OR NEW.placed_by IS DISTINCT FROM OLD.placed_by OR NEW.placed_at IS DISTINCT FROM OLD.placed_at OR OLD.released_at IS NOT NULL OR NEW.released_at IS NULL OR NEW.released_by IS NULL OR NEW.release_reason IS NULL OR length(btrim(NEW.release_reason)) NOT BETWEEN 1 AND 1000 THEN RAISE EXCEPTION 'hold history is immutable'; END IF;
 RETURN NEW;
END $$;
