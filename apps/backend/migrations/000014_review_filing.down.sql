DROP POLICY org_insert ON invoyis.categories;
DROP POLICY org_insert ON invoyis.properties;
DROP POLICY org_insert ON invoyis.paper_locations;
CREATE POLICY org_insert ON invoyis.categories FOR INSERT
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
CREATE POLICY org_insert ON invoyis.properties FOR INSERT
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
CREATE POLICY org_insert ON invoyis.paper_locations FOR INSERT
 WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
DROP TRIGGER review_filing_active ON invoyis.document_revisions;
DROP FUNCTION invoyis.check_active_review_filing();
ALTER TABLE invoyis.document_revisions
 DROP CONSTRAINT revision_supplier_same_archive,
 DROP CONSTRAINT revision_category_same_archive,
 DROP CONSTRAINT revision_property_same_archive,
 DROP CONSTRAINT revision_paper_location_same_archive,
 DROP COLUMN supplier_id,
 DROP COLUMN category_id,
 DROP COLUMN property_id,
 DROP COLUMN paper_location_id,
 DROP COLUMN reference_scan;
