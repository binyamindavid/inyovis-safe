DROP TABLE invoyis.supplier_identity_keys;
DROP TABLE invoyis.paper_locations;
DROP TABLE invoyis.properties;
DROP TABLE invoyis.categories;
DROP TABLE invoyis.suppliers;
DROP FUNCTION IF EXISTS invoyis.check_supplier_registry();
DROP FUNCTION IF EXISTS invoyis.sync_supplier_canonical();
DROP FUNCTION IF EXISTS invoyis.check_active_alias_target();
DROP FUNCTION IF EXISTS invoyis.organisation_immutable_scope();
