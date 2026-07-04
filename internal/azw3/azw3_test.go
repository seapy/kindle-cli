package azw3

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seapy/kindle-cli/internal/epub"
	"github.com/seapy/kindle-cli/internal/patch"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testBook(t *testing.T) *epub.Book {
	t.Helper()
	return &epub.Book{
		Title:    "테스트 책",
		Author:   "홍길동",
		Language: "ko-KR",
		Chapters: []epub.Chapter{
			{
				Path:  "EPUB/ch1.xhtml",
				Title: "1장",
				HTML: []byte(`<html><head><title>c1</title></head><body class="main">
					<h1>첫 장</h1><p>본문입니다.</p>
					<img src="image/pic.png" alt="pic"/>
					<a href="ch2.xhtml#note1">주석</a>
					<a href="https://example.com">외부 링크</a>
					<script>alert("nope")</script>
				</body></html>`),
			},
			{
				Path: "EPUB/ch2.xhtml",
				HTML: []byte(`<html><head><title>c2</title></head><body><p>둘째 장</p></body></html>`),
			},
		},
		CSS:       []string{`@font-face { font-family: "X"; src: url(f.ttf); } p { margin: 0; }`},
		Images:    map[string][]byte{"EPUB/image/pic.png": nil, "EPUB/cover.jpg": nil},
		CoverPath: "EPUB/cover.jpg",
	}
}

func TestWriteProducesPDOCAZW3(t *testing.T) {
	book := testBook(t)
	book.Images["EPUB/image/pic.png"] = testPNG(t, 40, 30)
	book.Images["EPUB/cover.jpg"] = testPNG(t, 200, 900)

	out := filepath.Join(t.TempDir(), "book.azw3")
	warnings, err := Write(book, out, Options{})
	if err != nil {
		t.Fatalf("Write: %v (warnings: %v)", err, warnings)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 68 || string(data[60:68]) != "BOOKMOBI" {
		t.Fatalf("output is not a PalmDB MOBI container")
	}
	_, cdetype, err := patch.FindCDEType(data)
	if err != nil {
		t.Fatalf("FindCDEType: %v", err)
	}
	if !bytes.Equal(cdetype, []byte("PDOC")) {
		t.Errorf("cdetype = %q, want PDOC", cdetype)
	}
}

func TestWriteKeepEBOK(t *testing.T) {
	book := testBook(t)
	book.Images["EPUB/image/pic.png"] = testPNG(t, 40, 30)
	book.Images["EPUB/cover.jpg"] = testPNG(t, 200, 300)

	out := filepath.Join(t.TempDir(), "book.azw3")
	if _, err := Write(book, out, Options{DocType: "EBOK"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(out)
	_, cdetype, err := patch.FindCDEType(data)
	if err != nil {
		t.Fatalf("FindCDEType: %v", err)
	}
	if !bytes.Equal(cdetype, []byte("EBOK")) {
		t.Errorf("cdetype = %q, want EBOK", cdetype)
	}
}

func TestWriteWarnsOnMissingCover(t *testing.T) {
	book := testBook(t)
	book.Images["EPUB/image/pic.png"] = testPNG(t, 40, 30)
	book.CoverPath = ""
	delete(book.Images, "EPUB/cover.jpg")

	out := filepath.Join(t.TempDir(), "book.azw3")
	warnings, err := Write(book, out, Options{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "cover") {
		t.Errorf("expected a missing-cover warning, got %v", warnings)
	}
}

func TestChapterBodyRewriting(t *testing.T) {
	book := testBook(t)
	book.Images["EPUB/image/pic.png"] = testPNG(t, 40, 30)
	c := &converter{book: book, imageIndex: map[string]int{}}

	chunks, err := c.chapterChunks(book.Chapters[0])
	if err != nil {
		t.Fatalf("chapterChunks: %v", err)
	}
	body := strings.Join(chunks, "")
	if !strings.Contains(body, `src="kindle:embed:0001?mime=image/jpeg"`) {
		t.Errorf("img not rewritten to kindle:embed: %s", body)
	}
	if strings.Contains(body, "ch2.xhtml") {
		t.Errorf("internal link not flattened: %s", body)
	}
	if !strings.Contains(body, "주석") {
		t.Errorf("flattened link lost its text: %s", body)
	}
	if !strings.Contains(body, `href="https://example.com"`) {
		t.Errorf("external link should be kept: %s", body)
	}
	if strings.Contains(body, "<script") {
		t.Errorf("script not removed: %s", body)
	}
	if !strings.Contains(body, `<div class="main">`) {
		t.Errorf("body class not carried onto wrapper: %s", body)
	}
}

func TestSVGCoverBecomesImg(t *testing.T) {
	book := &epub.Book{
		Images: map[string][]byte{"EPUB/cover.png": testPNG(t, 10, 10)},
	}
	c := &converter{book: book, imageIndex: map[string]int{}}
	ch := epub.Chapter{
		Path: "EPUB/titlepage.xhtml",
		HTML: []byte(`<html><body><svg xmlns="http://www.w3.org/2000/svg">
			<image xlink:href="cover.png" width="10" height="10"/></svg></body></html>`),
	}
	chunks, err := c.chapterChunks(ch)
	if err != nil {
		t.Fatalf("chapterChunks: %v", err)
	}
	body := strings.Join(chunks, "")
	if !strings.Contains(body, "kindle:embed:0001") {
		t.Errorf("svg image not rewritten: %s", body)
	}
	if strings.Contains(body, "<svg") {
		t.Errorf("svg wrapper should be replaced: %s", body)
	}
}

func TestUndecodableImageDropped(t *testing.T) {
	book := &epub.Book{Images: map[string][]byte{"EPUB/bad.webp": []byte("not an image")}}
	c := &converter{book: book, imageIndex: map[string]int{}}
	ch := epub.Chapter{
		Path: "EPUB/ch.xhtml",
		HTML: []byte(`<html><body><p>text</p><img src="bad.webp"/></body></html>`),
	}
	chunks, err := c.chapterChunks(ch)
	if err != nil {
		t.Fatalf("chapterChunks: %v", err)
	}
	body := strings.Join(chunks, "")
	if strings.Contains(body, "bad.webp") {
		t.Errorf("broken image reference kept: %s", body)
	}
	if len(c.warnings) == 0 {
		t.Error("expected a warning for the undecodable image")
	}
}

func TestStripFontFaces(t *testing.T) {
	css := `@font-face { font-family: A; src: url(a.ttf); } p { color: red; } @font-face{font-family:B;}`
	got := stripFontFaces(css)
	if strings.Contains(got, "@font-face") {
		t.Errorf("font-face not stripped: %s", got)
	}
	if !strings.Contains(got, "p { color: red; }") {
		t.Errorf("other rules must survive: %s", got)
	}
}
