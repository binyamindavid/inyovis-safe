ALTER TABLE invoyis.processing_jobs DROP CONSTRAINT processing_jobs_failure_code_check;
ALTER TABLE invoyis.processing_jobs ADD CONSTRAINT processing_jobs_failure_code_check
 CHECK (failure_code IS NULL OR failure_code IN (
  'storage_unavailable','scanner_unavailable','original_integrity_mismatch','scan_rejected',
  'parser_timeout','parser_resource_exhausted','invalid_pdf','unsafe_xml','content_limit_exceeded',
  'lease_expired_exhausted','parser_attempts_exhausted','ocr_engine_unavailable'));
