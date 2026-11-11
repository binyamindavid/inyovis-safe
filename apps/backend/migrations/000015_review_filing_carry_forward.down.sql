CREATE OR REPLACE FUNCTION invoyis.check_active_review_filing() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.supplier_id IS NOT NULL AND NOT EXISTS
  (SELECT 1 FROM invoyis.suppliers WHERE archive_id=NEW.archive_id AND id=NEW.supplier_id AND archived_at IS NULL FOR SHARE) THEN
  RAISE EXCEPTION 'inactive supplier selection' USING ERRCODE='23514';
 END IF;
 IF NEW.category_id IS NOT NULL AND NOT EXISTS
  (SELECT 1 FROM invoyis.categories WHERE archive_id=NEW.archive_id AND id=NEW.category_id AND archived_at IS NULL FOR SHARE) THEN
  RAISE EXCEPTION 'inactive category selection' USING ERRCODE='23514';
 END IF;
 IF NEW.property_id IS NOT NULL AND NOT EXISTS
  (SELECT 1 FROM invoyis.properties WHERE archive_id=NEW.archive_id AND id=NEW.property_id AND archived_at IS NULL FOR SHARE) THEN
  RAISE EXCEPTION 'inactive property selection' USING ERRCODE='23514';
 END IF;
 IF NEW.paper_location_id IS NOT NULL AND NOT EXISTS
  (SELECT 1 FROM invoyis.paper_locations WHERE archive_id=NEW.archive_id AND id=NEW.paper_location_id AND archived_at IS NULL FOR SHARE) THEN
  RAISE EXCEPTION 'inactive paper location selection' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
