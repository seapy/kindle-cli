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
