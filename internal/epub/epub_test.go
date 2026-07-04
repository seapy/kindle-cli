package epub

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const testOPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">test-001</dc:identifier>
    <dc:title>테스트 책</dc:title>
    <dc:creator>홍길동</dc:creator>
    <dc:language>ko-KR</dc:language>
    <meta name="cover" content="cover-img"/>
  </metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="css" href="style/main.css" media-type="text/css"/>
    <item id="cover-img" href="image/cover%20art.jpg" media-type="image/jpeg"/>
    <item id="ch1" href="contents/ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="contents/ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`

const testNCX = `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint id="n1" playOrder="1">
      <navLabel><text>1장 시작</text></navLabel>
      <content src="contents/ch1.xhtml"/>
    </navPoint>
    <navPoint id="n2" playOrder="2">
      <navLabel><text>2장</text></navLabel>
      <content src="contents/ch2.xhtml#frag"/>
    </navPoint>
  </navMap>
</ncx>`

func writeTestEPUB(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="EPUB/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	add("EPUB/content.opf", testOPF)
	add("EPUB/toc.ncx", testNCX)
	add("EPUB/style/main.css", "p { margin: 0; }")
	add("EPUB/image/cover art.jpg", "\xff\xd8fakejpeg")
	add("EPUB/contents/ch1.xhtml", `<html><head><title>c1</title></head><body><p>one</p></body></html>`)
	add("EPUB/contents/ch2.xhtml", `<html><head><title>c2</title></head><body><p>two</p></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.epub")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadMetadata(t *testing.T) {
	book, err := Read(writeTestEPUB(t))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if book.Title != "테스트 책" {
		t.Errorf("Title = %q", book.Title)
	}
	if book.Author != "홍길동" {
		t.Errorf("Author = %q", book.Author)
	}
	if book.Language != "ko-KR" {
		t.Errorf("Language = %q", book.Language)
	}
}

func TestReadSpineAndTitles(t *testing.T) {
	book, err := Read(writeTestEPUB(t))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(book.Chapters))
	}
	if book.Chapters[0].Path != "EPUB/contents/ch1.xhtml" {
		t.Errorf("chapter 0 path = %q", book.Chapters[0].Path)
	}
	if book.Chapters[0].Title != "1장 시작" {
		t.Errorf("chapter 0 title = %q", book.Chapters[0].Title)
	}
	// NCX src with a fragment still maps to the file
	if book.Chapters[1].Title != "2장" {
		t.Errorf("chapter 1 title = %q", book.Chapters[1].Title)
	}
}

func TestReadCoverAndResources(t *testing.T) {
	book, err := Read(writeTestEPUB(t))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// percent-encoded href resolves to the real zip path
	if book.CoverPath != "EPUB/image/cover art.jpg" {
		t.Errorf("CoverPath = %q", book.CoverPath)
	}
	if _, ok := book.Images["EPUB/image/cover art.jpg"]; !ok {
		t.Error("cover image data not collected")
	}
	if len(book.CSS) != 1 || book.CSS[0] != "p { margin: 0; }" {
		t.Errorf("CSS = %q", book.CSS)
	}
}

func TestResolveRelative(t *testing.T) {
	cases := []struct{ from, ref, want string }{
		{"EPUB/contents/ch1.xhtml", "../image/a.jpg", "EPUB/image/a.jpg"},
		{"EPUB/contents/ch1.xhtml", "b.xhtml#frag", "EPUB/contents/b.xhtml"},
		{"content.opf", "ch1.xhtml", "ch1.xhtml"},
		{"EPUB/toc.ncx", "contents/ch%201.xhtml", "EPUB/contents/ch 1.xhtml"},
	}
	for _, c := range cases {
		if got := ResolveRelative(c.from, c.ref); got != c.want {
			t.Errorf("ResolveRelative(%q, %q) = %q, want %q", c.from, c.ref, got, c.want)
		}
	}
}

func TestReadRejectsNonEPUB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.epub")
	os.WriteFile(path, []byte("not a zip"), 0o644)
	if _, err := Read(path); err == nil {
		t.Error("expected error for non-zip input")
	}
}
