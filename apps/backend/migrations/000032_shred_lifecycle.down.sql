-- A successful shred has already erased bytes and metadata. Rolling back this
-- schema cannot restore them; use a forward repair with the disposal journal.
DO $$ BEGIN RAISE EXCEPTION 'document shredding lifecycle is irreversible'; END $$;
