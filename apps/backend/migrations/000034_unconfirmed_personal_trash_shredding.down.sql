-- Keep the ledger revision nullable: authorized personal Trash shreds can leave
-- durable NULL revisions even after their document and original are removed.
DROP FUNCTION invoyis.authorize_document_shred(uuid,uuid,text,uuid,uuid,text,timestamptz);
