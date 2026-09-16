package checks

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const RuleVersion = "m4-text-v1"

type Result struct {
	Status           string   `json:"status"`
	SHA256           string   `json:"sha256"`
	CheckedBytes     int64    `json:"checked_bytes"`
	CoverageComplete bool     `json:"coverage_complete"`
	Encoding         string   `json:"encoding"`
	BOM              bool     `json:"bom"`
	DetectedType     string   `json:"detected_type"`
	RiskCodes        []string `json:"risk_codes"`
}

var signatures = []struct {
	name   string
	prefix []byte
}{{"PE", []byte("MZ")}, {"ELF", []byte{0x7f, 'E', 'L', 'F'}}, {"ZIP", []byte{'P', 'K', 3, 4}}, {"PNG", []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10}}, {"JPEG", []byte{0xff, 0xd8, 0xff}}, {"PDF", []byte("%PDF-")}, {"GZIP", []byte{0x1f, 0x8b}}}

func InspectFile(direction, filename string, r io.Reader) (Result, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if direction == "PROD_TO_OFFICE" && ext == ".xlsx" {
		return inspectXLSX(r)
	}
	if direction == "OFFICE_TO_PROD" || ext == ".txt" || ext == ".log" || ext == ".csv" || ext == ".json" || ext == ".sql" {
		return InspectText(r)
	}
	h := sha256.New()
	header := make([]byte, 16)
	n, err := io.ReadFull(r, header)
	if err != nil && err != io.ErrUnexpectedEOF {
		return Result{Status: "ERROR"}, err
	}
	header = header[:n]
	status, risks := InspectType(filename, header)
	count, copyErr := io.Copy(h, io.MultiReader(bytes.NewReader(header), r))
	if copyErr != nil {
		return Result{Status: "ERROR", CheckedBytes: count, CoverageComplete: false}, copyErr
	}
	return Result{Status: status, SHA256: hex.EncodeToString(h.Sum(nil)), CheckedBytes: count, CoverageComplete: true, DetectedType: strings.TrimPrefix(strings.ToUpper(ext), "."), RiskCodes: risks}, nil
}
func inspectXLSX(r io.Reader) (Result, error) {
	tmp, err := os.CreateTemp("", "yundu-xlsx-*")
	if err != nil {
		return Result{Status: "ERROR"}, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	defer tmp.Close()
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, (100<<20)+1))
	if err != nil {
		return Result{Status: "ERROR", CheckedBytes: size}, err
	}
	out := Result{Status: "PASSED", SHA256: hex.EncodeToString(h.Sum(nil)), CheckedBytes: size, CoverageComplete: true, DetectedType: "XLSX", RiskCodes: []string{}}
	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		out.Status = "FAILED"
		out.RiskCodes = []string{"INVALID_OOXML"}
		return out, nil
	}
	if len(zr.File) > 10000 {
		out.Status = "FAILED"
		out.RiskCodes = append(out.RiskCodes, "CONTAINER_ENTRY_LIMIT")
	}
	contentTypes, workbook := false, false
	var expanded uint64
	for _, f := range zr.File {
		path := strings.ToLower(f.Name)
		expanded += f.UncompressedSize64
		if f.Flags&1 != 0 {
			out.Status = "FAILED"
			out.RiskCodes = appendUnique(out.RiskCodes, "ENCRYPTED_CONTAINER")
		}
		if path == "[content_types].xml" {
			contentTypes = true
		}
		if path == "xl/workbook.xml" {
			workbook = true
		}
		if strings.Contains(path, "vbaproject.bin") || strings.HasPrefix(path, "xl/embeddings/") {
			out.Status = "FAILED"
			out.RiskCodes = appendUnique(out.RiskCodes, "ACTIVE_CONTENT")
		}
	}
	if expanded > 512<<20 {
		out.Status = "FAILED"
		out.RiskCodes = appendUnique(out.RiskCodes, "CONTAINER_EXPANDED_LIMIT")
	}
	if !contentTypes || !workbook {
		out.Status = "FAILED"
		out.RiskCodes = appendUnique(out.RiskCodes, "INVALID_OOXML_STRUCTURE")
	}
	return out, nil
}
func InspectText(r io.Reader) (Result, error) {
	h := sha256.New()
	reader := bufio.NewReaderSize(io.TeeReader(r, h), 64*1024)
	out := Result{Status: "PASSED", Encoding: "UTF-8", CoverageComplete: true, DetectedType: "TEXT", RiskCodes: []string{}}
	first := true
	carry := []byte{}
	header := []byte{}
	for {
		chunk := make([]byte, 64*1024)
		n, err := reader.Read(chunk)
		if n > 0 {
			out.CheckedBytes += int64(n)
			if len(header) < 16 {
				need := 16 - len(header)
				if need > n {
					need = n
				}
				header = append(header, chunk[:need]...)
			}
			data := append(carry, chunk[:n]...)
			carry = nil
			if first {
				first = false
				if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
					out.BOM = true
					data = data[3:]
				}
			}
			for len(data) > 0 {
				if !utf8.FullRune(data) {
					carry = append(carry, data...)
					break
				}
				runeValue, size := utf8.DecodeRune(data)
				if runeValue == utf8.RuneError && size == 1 {
					out.RiskCodes = appendUnique(out.RiskCodes, "INVALID_UTF8")
					out.Status = "FAILED"
					data = data[1:]
					continue
				}
				if runeValue == 0 || (runeValue < 0x20 && runeValue != '\t' && runeValue != '\r' && runeValue != '\n') || runeValue == 0x7f {
					out.RiskCodes = appendUnique(out.RiskCodes, "CONTROL_BYTE")
					out.Status = "FAILED"
				}
				data = data[size:]
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			out.CoverageComplete = false
			out.Status = "ERROR"
			out.SHA256 = hex.EncodeToString(h.Sum(nil))
			return out, err
		}
	}
	if len(carry) > 0 {
		out.RiskCodes = appendUnique(out.RiskCodes, "TRUNCATED_UTF8")
		out.Status = "FAILED"
	}
	for _, sig := range signatures {
		if bytes.HasPrefix(header, sig.prefix) {
			out.DetectedType = sig.name
			out.RiskCodes = appendUnique(out.RiskCodes, "BINARY_SIGNATURE_"+sig.name)
			out.Status = "FAILED"
			break
		}
	}
	if out.CheckedBytes == 0 {
		out.RiskCodes = append(out.RiskCodes, "EMPTY_FILE")
		out.Status = "FAILED"
	}
	out.SHA256 = hex.EncodeToString(h.Sum(nil))
	return out, nil
}
func InspectType(filename string, header []byte) (string, []string) {
	ext := strings.ToLower(filepath.Ext(filename))
	allowed := map[string]bool{".txt": true, ".log": true, ".csv": true, ".json": true, ".sql": true, ".pdf": true, ".xlsx": true}
	if !allowed[ext] {
		return "FAILED", []string{"EXTENSION_NOT_ALLOWED"}
	}
	for _, sig := range signatures {
		if bytes.HasPrefix(header, sig.prefix) {
			if (ext == ".pdf" && sig.name == "PDF") || (ext == ".xlsx" && sig.name == "ZIP") {
				return "PASSED", nil
			}
			if sig.name != "PDF" && sig.name != "ZIP" {
				return "FAILED", []string{"BINARY_SIGNATURE_" + sig.name}
			}
		}
	}
	if ext == ".pdf" || ext == ".xlsx" {
		return "FAILED", []string{"TYPE_SIGNATURE_MISMATCH"}
	}
	return "PASSED", nil
}
func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}
