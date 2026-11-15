ALTER TABLE invoyis.document_revisions DROP CONSTRAINT document_revisions_non_invoice_has_no_amounts;
ALTER TABLE invoyis.document_revisions ADD CONSTRAINT document_revisions_non_invoice_has_no_amounts
 CHECK (metadata->>'documentType' NOT IN ('lease','court_correspondence','other')
        OR (subtotal IS NULL AND stated_tax IS NULL AND total IS NULL
            AND NOT (metadata ? 'supplier' OR metadata ? 'invoiceId' OR metadata ? 'accountReference'
                     OR metadata ? 'currency' OR metadata ? 'subtotal' OR metadata ? 'tax'
                     OR metadata ? 'total' OR metadata ? 'taxLines')));
