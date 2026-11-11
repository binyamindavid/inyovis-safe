ALTER TABLE invoyis.document_revisions
 ADD COLUMN supplier_id uuid,
 ADD COLUMN category_id uuid,
 ADD COLUMN property_id uuid,
 ADD COLUMN paper_location_id uuid,
 ADD COLUMN reference_scan boolean NOT NULL DEFAULT false,
 ADD CONSTRAINT revision_supplier_same_archive FOREIGN KEY (archive_id,supplier_id) REFERENCES invoyis.suppliers(archive_id,id),
 ADD CONSTRAINT revision_category_same_archive FOREIGN KEY (archive_id,category_id) REFERENCES invoyis.categories(archive_id,id),
 ADD CONSTRAINT revision_property_same_archive FOREIGN KEY (archive_id,property_id) REFERENCES invoyis.properties(archive_id,id),
 ADD CONSTRAINT revision_paper_location_same_archive FOREIGN KEY (archive_id,paper_location_id) REFERENCES invoyis.paper_locations(archive_id,id);

-- Historical links survive archival; only newly inserted selections must be active.
CREATE FUNCTION invoyis.check_active_review_filing() RETURNS trigger LANGUAGE plpgsql
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
CREATE TRIGGER review_filing_active BEFORE INSERT ON invoyis.document_revisions
 FOR EACH ROW EXECUTE FUNCTION invoyis.check_active_review_filing();

-- Contributors may create filing labels while filing; supplier identity creation
-- remains an admin action. The API still checks membership and audit context.
DROP POLICY org_insert ON invoyis.categories;
DROP POLICY org_insert ON invoyis.properties;
DROP POLICY org_insert ON invoyis.paper_locations;
CREATE POLICY org_insert ON invoyis.categories FOR INSERT
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
CREATE POLICY org_insert ON invoyis.properties FOR INSERT
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
CREATE POLICY org_insert ON invoyis.paper_locations FOR INSERT
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.can_submit(archive_id));
