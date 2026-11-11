package rendering

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func largeValidPDF() []byte {
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	object := func(number int, content string) {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", number, content)
	}
	object(1, "<< /Type /Catalog /Pages 2 0 R >>")
	object(2, "<< /Type /Pages /Count 1 /Kids [3 0 R] >>")
	object(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>")
	object(4, "<< /Length 0 >>\nstream\n\nendstream")
	offsets = append(offsets, pdf.Len())
	fmt.Fprintf(&pdf, "5 0 obj\n<< /Length %d >>\nstream\n", 6<<20)
	pdf.Write(bytes.Repeat([]byte("x"), 6<<20))
	pdf.WriteString("\nendstream\nendobj\n")
	xref := pdf.Len()
	pdf.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return pdf.Bytes()
}

func TestRenderMainAcceptsSixMiBPDF(t *testing.T) {
	if os.Getenv("INVOYIS_RENDER_TEST_CHILD") == "1" {
		os.Exit(RenderMain(1))
	}
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("Poppler not installed")
	}
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("Poppler not installed")
	}
	pdf := largeValidPDF()
	if len(pdf) <= 5<<20 || len(pdf) >= 25<<20 {
		t.Fatalf("fixture size %d", len(pdf))
	}
	command := exec.Command(os.Args[0], "-test.run=TestRenderMainAcceptsSixMiBPDF")
	command.Env = append(os.Environ(), "INVOYIS_RENDER_TEST_CHILD=1")
	command.Stdin = bytes.NewReader(pdf)
	var output bytes.Buffer
	command.Stdout = &output
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("valid large PDF failed: %v %s", err, stderr.String())
	}
	if !bytes.HasPrefix(output.Bytes(), []byte("1\n\x89PNG\r\n\x1a\n")) {
		t.Fatalf("not a page PNG: %x", output.Bytes()[:min(output.Len(), 16)])
	}
}
