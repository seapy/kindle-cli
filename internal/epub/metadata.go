package epub

import (
	"path/filepath"
	"strings"
)

// Metadata is the resolved title/author for a book, with flags reporting
// which fields were backfilled (true when the EPUB itself lacked the field).
type Metadata struct {
	Title            string
	Author           string
	TitleBackfilled  bool
	AuthorBackfilled bool
}

// DeriveMetadataFromFilename guesses (title, author) from a
// "Title - Author.epub" filename.
func DeriveMetadataFromFilename(epubPath string) (title, author string) {
	stem := strings.TrimSuffix(filepath.Base(epubPath), filepath.Ext(epubPath))
	if i := strings.LastIndex(stem, " - "); i >= 0 {
		return strings.TrimSpace(stem[:i]), strings.TrimSpace(stem[i+3:])
	}
	return strings.TrimSpace(stem), ""
}

// ResolveMetadata picks the title/author to use for the converted book.
//
// Priority: explicit override → EPUB metadata → filename guess. Some
// DRM-stripped EPUBs ship with an empty OPF (blank title/author); the
// filename guess keeps those books labeled.
func ResolveMetadata(book *Book, epubPath, titleOverride, authorOverride string) Metadata {
	fileTitle, fileAuthor := DeriveMetadataFromFilename(epubPath)
	m := Metadata{Title: titleOverride, Author: authorOverride}
	if m.Title == "" {
		m.Title = book.Title
	}
	if m.Title == "" {
		m.Title = fileTitle
	}
	if m.Author == "" {
		m.Author = book.Author
	}
	if m.Author == "" {
		m.Author = fileAuthor
	}
	m.TitleBackfilled = book.Title == "" && m.Title != ""
	m.AuthorBackfilled = book.Author == "" && m.Author != ""
	return m
}
