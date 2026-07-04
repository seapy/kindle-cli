package epub

import "testing"

func TestDeriveMetadataFromFilename(t *testing.T) {
	cases := []struct{ path, title, author string }{
		{"/x/Flash Boys - Michael Lewis.epub", "Flash Boys", "Michael Lewis"},
		{"/x/프로젝트 헤일메리 - 앤디 위어.epub", "프로젝트 헤일메리", "앤디 위어"},
		{"/x/No Author Here.epub", "No Author Here", ""},
		{"/x/A - B - C.epub", "A - B", "C"},
	}
	for _, c := range cases {
		title, author := DeriveMetadataFromFilename(c.path)
		if title != c.title || author != c.author {
			t.Errorf("DeriveMetadataFromFilename(%q) = (%q, %q), want (%q, %q)",
				c.path, title, author, c.title, c.author)
		}
	}
}

func TestResolveMetadataPriority(t *testing.T) {
	book := &Book{Title: "OPF Title", Author: "OPF Author"}
	path := "/x/File Title - File Author.epub"

	m := ResolveMetadata(book, path, "", "")
	if m.Title != "OPF Title" || m.Author != "OPF Author" {
		t.Errorf("EPUB metadata should win: %+v", m)
	}
	if m.TitleBackfilled || m.AuthorBackfilled {
		t.Errorf("nothing was backfilled: %+v", m)
	}

	m = ResolveMetadata(book, path, "Override", "")
	if m.Title != "Override" || m.Author != "OPF Author" {
		t.Errorf("override should win: %+v", m)
	}

	m = ResolveMetadata(&Book{}, path, "", "")
	if m.Title != "File Title" || m.Author != "File Author" {
		t.Errorf("filename should backfill: %+v", m)
	}
	if !m.TitleBackfilled || !m.AuthorBackfilled {
		t.Errorf("backfill flags should be set: %+v", m)
	}
}
