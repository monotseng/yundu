package checks

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestXLSXStructureAndMacroRejection(t *testing.T) {
	makeBook := func(macro bool) []byte {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, name := range []string{"[Content_Types].xml", "xl/workbook.xml"} {
			f, _ := w.Create(name)
			_, _ = f.Write([]byte("x"))
		}
		if macro {
			f, _ := w.Create("xl/vbaProject.bin")
			_, _ = f.Write([]byte("macro"))
		}
		_ = w.Close()
		return b.Bytes()
	}
	passed, _ := InspectFile("PROD_TO_OFFICE", "book.xlsx", bytes.NewReader(makeBook(false)))
	if passed.Status != "PASSED" {
		t.Fatalf("%+v", passed)
	}
	failed, _ := InspectFile("PROD_TO_OFFICE", "book.xlsx", bytes.NewReader(makeBook(true)))
	if failed.Status != "FAILED" || !contains(failed.RiskCodes, "ACTIVE_CONTENT") {
		t.Fatalf("%+v", failed)
	}
}

func TestInspectTextAcrossChunkBoundary(t *testing.T) {
	payload := append(bytes.Repeat([]byte{'a'}, 64*1024-1), []byte("中文\n\t")...)
	result, err := InspectText(bytes.NewReader(payload))
	if err != nil || result.Status != "PASSED" || !result.CoverageComplete || result.CheckedBytes != int64(len(payload)) {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestInspectTextRejectsInvalidTail(t *testing.T) {
	result, _ := InspectText(bytes.NewReader([]byte{'o', 'k', 0xe4, 0xb8}))
	if result.Status != "FAILED" || !contains(result.RiskCodes, "TRUNCATED_UTF8") {
		t.Fatalf("%+v", result)
	}
}
func TestInspectTextRejectsControlsAndRenamedBinary(t *testing.T) {
	for _, payload := range [][]byte{[]byte("ok\x00bad"), append([]byte("PK\x03\x04"), []byte(strings.Repeat("a", 100))...)} {
		result, _ := InspectText(bytes.NewReader(payload))
		if result.Status != "FAILED" {
			t.Fatalf("accepted %q", payload)
		}
	}
}
func TestBOMHashCoversOriginalBytes(t *testing.T) {
	payload := append([]byte{0xef, 0xbb, 0xbf}, []byte("你好")...)
	result, _ := InspectText(bytes.NewReader(payload))
	if !result.BOM || result.Status != "PASSED" || result.CheckedBytes != int64(len(payload)) {
		t.Fatalf("%+v", result)
	}
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
