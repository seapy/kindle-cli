// Package patch flips a MOBI/AZW3 file's cdetype (EXTH record 501) from EBOK
// to PDOC.
//
// Modern Kindles (2024+) grey out the library cover of sideloaded books tagged
// EBOK because they can't be matched to a Kindle Store ASIN, and Amazon has
// been observed auto-deleting EBOK sideloads. Re-tagging the file as a
// personal document (PDOC) makes the device use the file's own embedded cover
// and treats it as user content that is never auto-removed.
//
// EBOK and PDOC are both 4 ASCII bytes, so the value is replaced in place
// without touching any offsets or lengths — no re-encoding, no dependencies.
//
// See docs/background.md for the full rationale.
package patch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"strings"
)

// EXTHCDEType is the EXTH record type that holds the "content type" tag
// (EBOK / PDOC).
const EXTHCDEType = 501

// PDOC is the cdetype value for a personal document.
var PDOC = []byte("PDOC")

var exthMagic = []byte("EXTH")

// record0Offset returns the byte offset of PalmDB record 0 (which holds the
// MOBI header).
func record0Offset(data []byte) (int, error) {
	if len(data) < 82 {
		return 0, errors.New("file too small to be a MOBI/AZW3 container")
	}
	return int(binary.BigEndian.Uint32(data[78:82])), nil
}

// forEachEXTH walks the file's EXTH records, calling fn with each record's
// type, the absolute offset of its value, and the value bytes. A true return
// from fn stops the walk. Errors when there is no parseable EXTH header.
func forEachEXTH(data []byte, fn func(rtype, valOffset int, value []byte) bool) error {
	rec0, err := record0Offset(data)
	if err != nil {
		return err
	}
	if rec0 < 0 || rec0 > len(data) {
		return errors.New("no EXTH header found (unexpected AZW3 structure)")
	}
	idx := bytes.Index(data[rec0:], exthMagic)
	if idx < 0 {
		return errors.New("no EXTH header found (unexpected AZW3 structure)")
	}
	exth := rec0 + idx
	if exth+12 > len(data) {
		return errors.New("truncated EXTH header")
	}
	count := int(binary.BigEndian.Uint32(data[exth+8 : exth+12]))
	p := exth + 12
	for i := 0; i < count; i++ {
		if p+8 > len(data) {
			return errors.New("truncated EXTH record")
		}
		rtype := int(binary.BigEndian.Uint32(data[p : p+4]))
		rlen := int(binary.BigEndian.Uint32(data[p+4 : p+8]))
		if rlen < 8 {
			return errors.New("corrupt EXTH record (length < 8)")
		}
		if p+rlen > len(data) {
			return errors.New("truncated EXTH record")
		}
		if fn(rtype, p+8, data[p+8:p+rlen]) {
			return nil
		}
		p += rlen
	}
	return nil
}

// FindCDEType locates the cdetype (EXTH 501) value. It returns the absolute
// byte offset of the value and a copy of the value bytes. It errors when
// there is no EXTH header or no cdetype record.
func FindCDEType(data []byte) (int, []byte, error) {
	offset := -1
	var value []byte
	err := forEachEXTH(data, func(rtype, valOffset int, val []byte) bool {
		if rtype == EXTHCDEType {
			offset = valOffset
			value = bytes.Clone(val)
			return true
		}
		return false
	})
	if err != nil {
		return 0, nil, err
	}
	if offset < 0 {
		return 0, nil, errors.New("no cdetype (EXTH 501) record present")
	}
	return offset, value, nil
}

// Summary is the quickly readable identity of a MOBI/AZW3 file. All fields
// are best-effort and empty when not present.
type Summary struct {
	Title   string // EXTH 503, falling back to the MOBI full name
	Author  string // EXTH 100 (multiple records joined with ", ")
	CDEType string // EXTH 501: "PDOC" / "EBOK"
}

// ReadSummary parses a Summary from the head of a MOBI/AZW3 file. Everything
// it needs lives in PalmDB record 0, so the first ~64 KiB of the file is
// enough; it never errors, returning a zero Summary for non-MOBI data.
func ReadSummary(data []byte) Summary {
	var s Summary
	var authors []string
	_ = forEachEXTH(data, func(rtype, _ int, value []byte) bool {
		switch rtype {
		case 100:
			authors = append(authors, string(value))
		case 503:
			s.Title = string(value)
		case EXTHCDEType:
			s.CDEType = string(value)
		}
		return false
	})
	s.Author = strings.Join(authors, ", ")
	if s.Title == "" {
		s.Title = mobiFullName(data)
	}
	return s
}

// mobiFullName reads the full book name the MOBI header points at
// (offset/length fields at record0+0x54/0x58, relative to record 0).
func mobiFullName(data []byte) string {
	rec0, err := record0Offset(data)
	if err != nil || rec0 < 0 || rec0+92 > len(data) {
		return ""
	}
	if !bytes.Equal(data[rec0+16:rec0+20], []byte("MOBI")) {
		return ""
	}
	off := int(binary.BigEndian.Uint32(data[rec0+84 : rec0+88]))
	length := int(binary.BigEndian.Uint32(data[rec0+88 : rec0+92]))
	start := rec0 + off
	if length <= 0 || length > 4096 || start < 0 || start+length > len(data) {
		return ""
	}
	return string(data[start : start+length])
}

// PatchCDETypeBytes returns data with cdetype set to newValue.
//
// newValue must be exactly 4 bytes so the replacement is length-preserving.
// If cdetype already equals newValue the input is returned unchanged.
func PatchCDETypeBytes(data, newValue []byte) ([]byte, error) {
	if len(newValue) != 4 {
		return nil, errors.New("cdetype value must be exactly 4 bytes")
	}
	offset, value, err := FindCDEType(data)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(value, newValue) {
		return data, nil
	}
	buf := bytes.Clone(data)
	copy(buf[offset:offset+4], newValue)
	return buf, nil
}

// PatchFile patches path in place and returns the resulting cdetype as a
// string. It is a no-op (no write) when the file is already tagged newValue.
func PatchFile(path string, newValue []byte) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	_, current, err := FindCDEType(data)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(current, newValue) {
		patched, err := PatchCDETypeBytes(data, newValue)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path, patched, 0o644); err != nil {
			return "", err
		}
	}
	return string(newValue), nil
}
