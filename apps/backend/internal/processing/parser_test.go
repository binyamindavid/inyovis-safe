package processing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func invoiceXML(profile, tax string) []byte {
	return []byte(`<?xml version="1.0"?><CrossIndustryInvoice>
 <ExchangedDocumentContext><GuidelineSpecifiedDocumentContextParameter><ID>` + profile + `</ID></GuidelineSpecifiedDocumentContextParameter></ExchangedDocumentContext>
 <ExchangedDocument><ID>0000123</ID><IssueDateTime><DateTimeString>20261110</DateTimeString></IssueDateTime></ExchangedDocument>
 <SupplyChainTradeTransaction><ApplicableHeaderTradeAgreement><SellerTradeParty><Name>Synthetic Supplier</Name></SellerTradeParty></ApplicableHeaderTradeAgreement>
 <ApplicableHeaderTradeSettlement><InvoiceCurrencyCode>EUR</InvoiceCurrencyCode><SpecifiedTradeSettlementHeaderMonetarySummation>
 <LineTotalAmount>100.00</LineTotalAmount>` + tax + `<GrandTotalAmount>119.00</GrandTotalAmount>
 </SpecifiedTradeSettlementHeaderMonetarySummation></ApplicableHeaderTradeSettlement></SupplyChainTradeTransaction></CrossIndustryInvoice>`)
}
func TestFacturXProfilesAndExactCandidates(t *testing.T) {
	profiles := map[string]string{"minimum": "urn:factur-x.eu:1p0:minimum", "basic_wl": "urn:factur-x.eu:1p0:basicwl", "basic": "urn:factur-x.eu:1p0:basic", "en_16931": "urn:cen.eu:en16931:2017", "extended": "urn:factur-x.eu:1p0:extended"}
	for expected, uri := range profiles {
		t.Run(expected, func(t *testing.T) {
			result, err := parseXML(invoiceXML(uri, `<TaxTotalAmount>0.00</TaxTotalAmount>`))
			if err != nil {
				t.Fatal(err)
			}
			if result.Profile != expected || result.Candidates["invoiceId"] != "0000123" || result.Candidates["tax"] != "0.00" || result.Candidates["subtotal"] != "100.00" {
				t.Fatalf("%+v", result)
			}
			if result.Provenance["invoiceId"] == nil {
				t.Fatal("missing provenance")
			}
		})
	}
	missing, err := parseXML(invoiceXML(profiles["minimum"], ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := missing.Candidates["tax"]; exists {
		t.Fatal("missing tax became zero")
	}
	unsupported, err := parseXML(invoiceXML("unrecognised-profile", ""))
	if err != nil || len(unsupported.Candidates) != 0 || unsupported.Warnings[0] != "unsupported_profile" {
		t.Fatalf("unsupported %+v %v", unsupported, err)
	}
	inconsistent, err := parseXML(invoiceXML(profiles["basic"], `<TaxTotalAmount>0.00</TaxTotalAmount>`))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range inconsistent.Warnings {
		if warning == "inconsistent_total" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing inconsistent-total warning")
	}
}
func TestUnsafeXMLAndLimits(t *testing.T) {
	for _, xml := range []string{
		`<!DOCTYPE x [<!ENTITY xx SYSTEM "file:///etc/passwd">]><x>&xx;</x>`,
		`<!DOCTYPE x [<!ENTITY a "b"><!ENTITY b "&a;&a;">]><x>&b;</x>`,
	} {
		if _, err := parseXML([]byte(xml)); err == nil {
			t.Fatal("accepted XML directive")
		}
	}
	if _, err := parseXML([]byte(strings.Repeat("x", maxXML+1))); err == nil {
		t.Fatal("accepted excessive XML")
	}
	if _, err := parsePDF(context.Background(), []byte("not PDF")); err == nil {
		t.Fatal("accepted invalid PDF")
	}
}

func TestSyntheticPDFAttachments(t *testing.T) {
	if _, err := exec.LookPath("pdfdetach"); err != nil {
		t.Skip("Poppler pdfdetach required")
	}
	for _, profile := range []string{"minimum", "basic_wl", "basic", "en_16931", "extended"} {
		t.Run(profile, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", profile+".pdf"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := parsePDF(context.Background(), data)
			if err != nil || result.Profile != profile || result.Candidates["invoiceId"] != "0000123" || result.Candidates["tax"] != "0.00" {
				t.Fatalf("result %+v err %v", result, err)
			}
		})
	}
	for name, want := range map[string]string{"ordinary": "no_supported_xml", "noncanonical": "no_supported_xml", "ambiguous": "ambiguous_invoice_xml", "unsupported": "unsupported_profile"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name+".pdf"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := parsePDF(context.Background(), data)
			if err != nil || len(result.Candidates) != 0 || len(result.Warnings) != 1 || result.Warnings[0] != want {
				t.Fatalf("result %+v err %v", result, err)
			}
		})
	}
	for name, want := range map[string]string{"unsafe_xml": "unsafe_xml", "oversized_xml": "content_limit_exceeded", "decompression_bomb": "content_limit_exceeded"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name+".pdf"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = parsePDF(context.Background(), data)
			failure, ok := err.(*ParseFailure)
			if !ok || failure.Code != want {
				t.Fatalf("failure %v want %s", err, want)
			}
		})
	}
}

func TestIsolatedParserRejectsDecompressionBomb(t *testing.T) {
	if os.Getenv("PROCESSING_ISOLATED_PARSER_TEST") != "1" {
		t.Skip("isolated parser integration check is opt-in")
	}
	binary := filepath.Join(t.TempDir(), "worker")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/worker")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build parser: %v: %s", err, output)
	}
	t.Setenv("PROCESSING_PARSER_BINARY", binary)
	ordinary, err := os.ReadFile(filepath.Join("testdata", "basic.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := ParseIsolated(context.Background(), ordinary)
	if err != nil || baseline.Profile != "basic" {
		t.Fatalf("isolated baseline = %+v, %v", baseline, err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "decompression_bomb.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseIsolated(context.Background(), data)
	failure, ok := err.(*ParseFailure)
	if !ok || failure.Code != "content_limit_exceeded" || failure.Retry {
		t.Fatalf("decompression bomb failure = %v", err)
	}
}
