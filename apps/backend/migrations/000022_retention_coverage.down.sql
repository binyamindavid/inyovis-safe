DROP TRIGGER retention_coverage_publication_guard ON invoyis.retention_active_rule_set;
DROP FUNCTION invoyis.retention_coverage_publication_guard();
DROP FUNCTION invoyis.validate_retention_coverage(text);
DROP TABLE invoyis.retention_coverage;
