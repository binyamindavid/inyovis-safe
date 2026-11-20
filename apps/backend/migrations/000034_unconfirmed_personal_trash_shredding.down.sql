DROP FUNCTION invoyis.authorize_document_shred(uuid,uuid,text,uuid,uuid,text,timestamptz);
ALTER TABLE invoyis.document_shreds ALTER COLUMN confirmed_revision_id SET NOT NULL;
