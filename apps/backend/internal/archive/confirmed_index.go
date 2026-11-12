package archive

import "strings"

// IndexColumns are the stable machine headers of the confirmed-metadata CSV index.
// Invoice columns stay present and blank for non-invoice records so every row has the
// same shape. Issue #9 owns the export endpoint, permission checks and file packaging;
// this file only maps one confirmed revision to one row, so both issues share one
// definition of the confirmed values. Never feed it a draft revision.
var IndexColumns = []string{
	"document_id", "document_type", "title", "sender", "document_date", "reference", "notes",
	"supplier", "invoice_id", "account_reference", "currency", "subtotal", "tax", "total",
	"filing_supplier_id", "filing_supplier_name", "filing_category_id", "filing_category_name",
	"filing_property_id", "filing_property_name", "filing_paper_location_id", "filing_paper_location_name",
	"reference_scan",
}

// ExportFiling contains archive-scoped, confirmed links and their current labels.
type ExportFiling struct {
	SupplierID, SupplierName, CategoryID, CategoryName           string
	PropertyID, PropertyName, PaperLocationID, PaperLocationName string
	ReferenceScan                                                bool
}

// IndexRow maps confirmed metadata to the values for IndexColumns. Amounts are exact
// decimal strings; unstated tax stays blank while an explicit zero stays "0.00".
func IndexRow(documentID string, m ReviewMetadata, filing ...ExportFiling) []string {
	row := make([]string, len(IndexColumns))
	row[0], row[1] = documentID, m.DocumentType
	row[4] = m.IssueDate
	if IsDocumentType(m.DocumentType) {
		row[2], row[3], row[5], row[6] = m.Title, m.Sender, m.Reference, m.Notes
	} else {
		row[7], row[8], row[9], row[10] = m.Supplier, m.InvoiceID, m.AccountReference, m.Currency
		for i, v := range []*string{m.Subtotal, m.Tax, m.Total} {
			if v != nil {
				row[11+i] = *v
			}
		}
	}
	if len(filing) != 0 {
		f := filing[0]
		copy(row[14:], []string{f.SupplierID, f.SupplierName, f.CategoryID, f.CategoryName,
			f.PropertyID, f.PropertyName, f.PaperLocationID, f.PaperLocationName})
		if f.ReferenceScan {
			row[22] = "true"
		}
	}
	for i := 1; i < len(row); i++ {
		row[i] = NeutraliseSpreadsheetCell(row[i])
	}
	return row
}

// NeutraliseSpreadsheetCell prefixes a quote when a cell could be read as a formula by a
// spreadsheet. Plain references with leading zeroes are unchanged and remain text in CSV.
func NeutraliseSpreadsheetCell(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '@', '\t', '\r':
		return "'" + v
	case '-':
		// A negative amount is data, not a formula; anything else starting with '-' is not.
		if reviewDecimal.MatchString(v) {
			return v
		}
		return "'" + v
	}
	if strings.HasPrefix(v, "\ufeff") {
		return "'" + v
	}
	return v
}
