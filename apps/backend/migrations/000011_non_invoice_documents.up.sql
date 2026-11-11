-- Non-invoice documents (lease, court_correspondence, other) share the review revision
-- table. The Go service rejects mixed submissions; this constraint is defence in depth so
-- a non-invoice revision can never carry the typed invoice columns.
ALTER TABLE invoyis.document_revisions ADD CONSTRAINT document_revisions_non_invoice_has_no_amounts
 CHECK (metadata->>'documentType' NOT IN ('lease','court_correspondence','other')
        OR (subtotal IS NULL AND stated_tax IS NULL AND total IS NULL
            AND NOT (metadata ? 'supplier' OR metadata ? 'invoiceId' OR metadata ? 'accountReference'
                     OR metadata ? 'currency' OR metadata ? 'subtotal' OR metadata ? 'tax'
                     OR metadata ? 'total' OR metadata ? 'taxLines')));
-- Confirmed list queries read the pointer and revision together.
CREATE INDEX IF NOT EXISTS documents_confirmed_revision ON invoyis.documents(archive_id,current_confirmed_revision_id);
