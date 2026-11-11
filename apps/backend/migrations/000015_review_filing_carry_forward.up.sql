CREATE OR REPLACE FUNCTION invoyis.check_active_review_filing() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE
 prior invoyis.document_revisions%ROWTYPE;
 current_prior uuid;
BEGIN
 -- Lock the document so a direct SQL writer cannot race the revision pointer.
 SELECT COALESCE(latest_draft_revision_id,current_confirmed_revision_id) INTO current_prior
 FROM invoyis.documents WHERE archive_id=NEW.archive_id AND id=NEW.document_id FOR UPDATE;
 IF NEW.preceding_revision_id IS NOT DISTINCT FROM current_prior AND current_prior IS NOT NULL THEN
  SELECT * INTO prior FROM invoyis.document_revisions
  WHERE archive_id=NEW.archive_id AND document_id=NEW.document_id AND id=current_prior;
 END IF;
 IF NEW.supplier_id IS NOT NULL AND NOT EXISTS
  (SELECT 1 FROM invoyis.suppliers WHERE archive_id=NEW.archive_id AND id=NEW.supplier_id AND archived_at IS NULL FOR SHARE)
  AND (prior.id IS NULL OR prior.supplier_id IS DISTINCT FROM NEW.supplier_id) THEN
  RAISE EXCEPTION 'inactive supplier selection' USING ERRCODE='23514';
 END IF;
 IF NEW.category_id IS NOT NULL AND NOT EXISTS
  (SELECT 1 FROM invoyis.categories WHERE archive_id=NEW.archive_id AND id=NEW.category_id AND archived_at IS NULL FOR SHARE)
  AND (prior.id IS NULL OR prior.category_id IS DISTINCT FROM NEW.category_id) THEN
  RAISE EXCEPTION 'inactive category selection' USING ERRCODE='23514';
 END IF;
 IF NEW.property_id IS NOT NULL AND NOT EXISTS
  (SELECT 1 FROM invoyis.properties WHERE archive_id=NEW.archive_id AND id=NEW.property_id AND archived_at IS NULL FOR SHARE)
  AND (prior.id IS NULL OR prior.property_id IS DISTINCT FROM NEW.property_id) THEN
  RAISE EXCEPTION 'inactive property selection' USING ERRCODE='23514';
 END IF;
 IF NEW.paper_location_id IS NOT NULL AND NOT EXISTS
  (SELECT 1 FROM invoyis.paper_locations WHERE archive_id=NEW.archive_id AND id=NEW.paper_location_id AND archived_at IS NULL FOR SHARE)
  AND (prior.id IS NULL OR prior.paper_location_id IS DISTINCT FROM NEW.paper_location_id) THEN
  RAISE EXCEPTION 'inactive paper location selection' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
