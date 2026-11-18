-- Disposal history is evidence and must survive rollback. Restore requires an explicit forward migration.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM invoyis.document_trash_events) OR EXISTS (SELECT 1 FROM invoyis.documents WHERE trashed_at IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot roll back document trash with disposal evidence';
 END IF;
END $$;
DROP FUNCTION invoyis.restore_document_from_trash(uuid,uuid);
DROP FUNCTION invoyis.move_document_to_trash(uuid,uuid,text);
DROP FUNCTION invoyis.can_trash(uuid),invoyis.can_restore(uuid),invoyis.can_shred(uuid);
DROP TABLE invoyis.document_trash_events;
DROP POLICY document_live_read ON invoyis.documents;
DROP INDEX invoyis.documents_trash_lookup;
ALTER TABLE invoyis.documents DROP CONSTRAINT document_trash_pair, DROP COLUMN trashed_at, DROP COLUMN recovery_until;
