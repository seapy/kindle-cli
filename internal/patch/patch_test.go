// Unit tests for the cdetype patcher — no Calibre or device required.
package patch

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// makeMobi builds a minimal PalmDB + EXTH blob the patcher can parse.
func makeMobi(cdetype []byte, includeCDEType bool) []byte {
	rec0 := 100

	u32 := func(v int) []byte {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(v))
		return b
	}

	var records []byte
	count := 0
	// a non-cdetype record first, to exercise iteration past other records
	author := []byte("tester")
	records = append(records, u32(100)...)
	records = append(records, u32(8+len(author))...)
	records = append(records, author...)
	count++
	if includeCDEType {
		records = append(records, u32(EXTHCDEType)...)
		records = append(records, u32(8+len(cdetype))...)
		records = append(records, cdetype...)
		count++
	}

	var exth []byte
	exth = append(exth, "EXTH"...)
	exth = append(exth, u32(12+len(records))...)
	exth = append(exth, u32(count)...)
	exth = append(exth, records...)

	buf := make([]byte, rec0+16+len(exth))
	binary.BigEndian.PutUint16(buf[76:78], 1)            // number of PDB records
	binary.BigEndian.PutUint32(buf[78:82], uint32(rec0)) // record 0 offset
	copy(buf[rec0:], "MOBIfakeheader__")                 // 16-byte stand-in MOBI header
	copy(buf[rec0+16:], exth)
	return buf
}

func TestFindCDETypeEBOK(t *testing.T) {
	data := makeMobi([]byte("EBOK"), true)
	offset, value, err := FindCDEType(data)
	if err != nil {
		t.Fatalf("FindCDEType: %v", err)
	}
	if !bytes.Equal(value, []byte("EBOK")) {
		t.Errorf("value = %q, want EBOK", value)
	}
	if !bytes.Equal(data[offset:offset+4], []byte("EBOK")) {
		t.Errorf("data at offset = %q, want EBOK", data[offset:offset+4])
	}
}

func TestPatchEBOKToPDOCIsLengthPreserving(t *testing.T) {
	data := makeMobi([]byte("EBOK"), true)
	patched, err := PatchCDETypeBytes(data, PDOC)
	if err != nil {
		t.Fatalf("PatchCDETypeBytes: %v", err)
	}
	_, value, err := FindCDEType(patched)
	if err != nil {
		t.Fatalf("FindCDEType after patch: %v", err)
	}
	if !bytes.Equal(value, PDOC) {
		t.Errorf("value = %q, want PDOC", value)
	}
	if len(patched) != len(data) {
		t.Errorf("len changed: %d → %d", len(data), len(patched))
	}
}

func TestPatchIsNoopWhenAlreadyPDOC(t *testing.T) {
	data := makeMobi([]byte("PDOC"), true)
	patched, err := PatchCDETypeBytes(data, PDOC)
	if err != nil {
		t.Fatalf("PatchCDETypeBytes: %v", err)
	}
	if !bytes.Equal(patched, data) {
		t.Error("expected unchanged data when already PDOC")
	}
}

func TestMissingCDETypeErrors(t *testing.T) {
	data := makeMobi(nil, false)
	if _, _, err := FindCDEType(data); err == nil {
		t.Error("expected error for missing cdetype record")
	}
}

func TestNewValueMustBeFourBytes(t *testing.T) {
	data := makeMobi([]byte("EBOK"), true)
	if _, err := PatchCDETypeBytes(data, []byte("PD")); err == nil {
		t.Error("expected error for 2-byte cdetype value")
	}
}

func TestTooSmallFileErrors(t *testing.T) {
	if _, _, err := FindCDEType(make([]byte, 40)); err == nil {
		t.Error("expected error for undersized container")
	}
}

func TestTruncatedEXTHErrors(t *testing.T) {
	data := makeMobi([]byte("EBOK"), true)
	// cut the file mid-EXTH so record iteration would run out of bounds
	if _, _, err := FindCDEType(data[:len(data)-6]); err == nil {
		t.Error("expected error for truncated EXTH record")
	}
}

func TestPatchFileRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.azw3")
	if err := os.WriteFile(path, makeMobi([]byte("EBOK"), true), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := PatchFile(path, PDOC)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if result != "PDOC" {
		t.Errorf("result = %q, want PDOC", result)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, value, err := FindCDEType(data)
	if err != nil {
		t.Fatalf("FindCDEType after roundtrip: %v", err)
	}
	if !bytes.Equal(value, PDOC) {
		t.Errorf("value = %q, want PDOC", value)
	}
}

// makeMobiFull builds a PalmDB blob with a MOBI header (full-name fields)
// plus EXTH author/title/cdetype records, mimicking a real AZW3 head.
func makeMobiFull(title string, authors []string, exthTitle string) []byte {
	rec0 := 100

	u32 := func(v int) []byte {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(v))
		return b
	}

	var records []byte
	count := 0
	add := func(rtype int, val []byte) {
		records = append(records, u32(rtype)...)
		records = append(records, u32(8+len(val))...)
		records = append(records, val...)
		count++
	}
	for _, a := range authors {
		add(100, []byte(a))
	}
	if exthTitle != "" {
		add(503, []byte(exthTitle))
	}
	add(EXTHCDEType, []byte("PDOC"))

	var exth []byte
	exth = append(exth, "EXTH"...)
	exth = append(exth, u32(12+len(records))...)
	exth = append(exth, u32(count)...)
	exth = append(exth, records...)

	// record 0: 16-byte PalmDOC stand-in, "MOBI" magic, header up to the
	// full-name fields at +84/+88, then EXTH, then the full name
	header := make([]byte, 92)
	copy(header[16:], "MOBI")
	fullnameOff := len(header) + len(exth)
	binary.BigEndian.PutUint32(header[84:88], uint32(fullnameOff))
	binary.BigEndian.PutUint32(header[88:92], uint32(len(title)))

	buf := make([]byte, rec0+len(header)+len(exth)+len(title))
	binary.BigEndian.PutUint16(buf[76:78], 1)
	binary.BigEndian.PutUint32(buf[78:82], uint32(rec0))
	copy(buf[rec0:], header)
	copy(buf[rec0+len(header):], exth)
	copy(buf[rec0+fullnameOff:], title)
	return buf
}

func TestReadSummaryFromEXTH(t *testing.T) {
	data := makeMobiFull("풀네임 제목", []string{"저자1", "저자2"}, "EXTH 제목")
	s := ReadSummary(data)
	if s.Title != "EXTH 제목" {
		t.Errorf("Title = %q, want the EXTH 503 value", s.Title)
	}
	if s.Author != "저자1, 저자2" {
		t.Errorf("Author = %q, want joined authors", s.Author)
	}
	if s.CDEType != "PDOC" {
		t.Errorf("CDEType = %q, want PDOC", s.CDEType)
	}
}

func TestReadSummaryFullNameFallback(t *testing.T) {
	data := makeMobiFull("풀네임 제목", nil, "")
	s := ReadSummary(data)
	if s.Title != "풀네임 제목" {
		t.Errorf("Title = %q, want the MOBI full name", s.Title)
	}
	if s.Author != "" {
		t.Errorf("Author = %q, want empty", s.Author)
	}
}

func TestReadSummaryGarbage(t *testing.T) {
	s := ReadSummary([]byte("not a mobi file at all"))
	if s != (Summary{}) {
		t.Errorf("Summary = %+v, want zero value", s)
	}
}
