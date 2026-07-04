package azw3

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/leotaku/mobi"

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
				HTML: []byte(`<html><head><title>c2</title></head><body><p>둘째 장</p><p id="note1">주석 내용</p></body></html>`),
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

// assembleBook runs the full pre-serialization pipeline and returns the
// patched chunk bodies plus the converter for warning/asset inspection.
func assembleBook(t *testing.T, book *epub.Book) ([]mobi.Chapter, *converter) {
	t.Helper()
	c := newConverter(book)
	chapters, err := c.assemble()
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return chapters, c
}

func joinBodies(chapters []mobi.Chapter) string {
	var sb strings.Builder
	for _, ch := range chapters {
		for _, chunk := range ch.Chunks {
			sb.WriteString(chunk.Body)
		}
	}
	return sb.String()
}

func TestChapterBodyRewriting(t *testing.T) {
	book := testBook(t)
	book.Images["EPUB/image/pic.png"] = testPNG(t, 40, 30)
	chapters, _ := assembleBook(t, book)

	body := joinBodies(chapters)
	if !strings.Contains(body, `src="kindle:embed:0001?mime=image/png"`) {
		t.Errorf("img not rewritten to kindle:embed: %s", body)
	}
	if strings.Contains(body, "ch2.xhtml") {
		t.Errorf("internal link not rewritten: %s", body)
	}
	if !strings.Contains(body, `href="kindle:pos:fid:`) {
		t.Errorf("internal link should become a position link: %s", body)
	}
	if !strings.Contains(body, "주석") {
		t.Errorf("rewritten link lost its text: %s", body)
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

var posLinkRe = regexp.MustCompile(`href="kindle:pos:fid:([0-9A-V]{4}):off:([0-9A-V]{10})"`)

// findPosLink returns the (fid, off) of the first position link in body.
func findPosLink(t *testing.T, body string) (int, int) {
	t.Helper()
	m := posLinkRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no position link found in: %s", body)
	}
	fid, err := strconv.ParseInt(m[1], 32, 64)
	if err != nil {
		t.Fatal(err)
	}
	off, err := strconv.ParseInt(m[2], 32, 64)
	if err != nil {
		t.Fatal(err)
	}
	return int(fid), int(off)
}

func TestFootnoteLinkResolvesToTargetTag(t *testing.T) {
	book := testBook(t)
	book.Images["EPUB/image/pic.png"] = testPNG(t, 4, 4)
	chapters, c := assembleBook(t, book)
	if len(c.warnings) != 0 {
		t.Errorf("unexpected warnings: %v", c.warnings)
	}
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(chapters))
	}

	// ch1's link must point into ch2's single chunk (global fid 1, since
	// ch1 is one chunk) at the exact byte of the <p id="note1"> start tag
	ch1 := joinBodies(chapters[:1])
	fid, off := findPosLink(t, ch1)
	if fid != 1 {
		t.Errorf("fid = %d, want 1 (first chunk of ch2)", fid)
	}
	target := chapters[1].Chunks[0].Body[off:]
	if !strings.HasPrefix(target, `<p id="note1">`) {
		t.Errorf("link points at %.40q, want the note1 tag", target)
	}
	if strings.Contains(ch1+joinBodies(chapters[1:]), "fid:ZZZZ") {
		t.Error("unpatched placeholder link left in output")
	}
}

func TestCrossChapterLinkIntoSplitChunk(t *testing.T) {
	// ch2 is large enough to split into several chunks, with the footnote
	// target deep inside; the link's fid must select the right chunk
	var ch2 strings.Builder
	ch2.WriteString("<html><body>")
	for i := 0; i < 100; i++ {
		if i == 80 {
			ch2.WriteString(`<p id="deep-note">깊은 주석</p>`)
			continue
		}
		ch2.WriteString("<p>" + strings.Repeat("본문. ", 40) + "</p>")
	}
	ch2.WriteString("</body></html>")

	book := &epub.Book{
		Title: "링크", Author: "저자",
		Chapters: []epub.Chapter{
			{Path: "EPUB/ch1.xhtml", HTML: []byte(`<html><body><p><a href="ch2.xhtml#deep-note">주석</a></p></body></html>`)},
			{Path: "EPUB/ch2.xhtml", HTML: []byte(ch2.String())},
		},
		Images: map[string][]byte{},
	}
	chapters, c := assembleBook(t, book)
	if len(c.warnings) != 0 {
		t.Errorf("unexpected warnings: %v", c.warnings)
	}
	fid, off := findPosLink(t, joinBodies(chapters[:1]))
	if fid <= 1 {
		t.Errorf("fid = %d, want a later chunk of the split ch2", fid)
	}
	// fid is global: chunk index within ch2 = fid - 1 (ch1 has one chunk)
	target := chapters[1].Chunks[fid-1].Body[off:]
	if !strings.HasPrefix(target, `<p id="deep-note">`) {
		t.Errorf("link points at %.40q, want the deep-note tag", target)
	}
}

func TestLinkFallbacks(t *testing.T) {
	book := &epub.Book{
		Title: "폴백", Author: "저자",
		Chapters: []epub.Chapter{
			{Path: "EPUB/ch1.xhtml", HTML: []byte(`<html><body>
				<p><a href="ch2.xhtml#missing">없는 주석</a></p>
				<p><a href="ch2.xhtml">파일 링크</a></p>
				<p><a href="nav.xhtml#toc">스파인 밖</a></p>
				<p><a href="#">맨 위로</a></p>
			</body></html>`)},
			{Path: "EPUB/ch2.xhtml", HTML: []byte(`<html><body><p>둘째 장</p></body></html>`)},
		},
		Images: map[string][]byte{},
	}
	chapters, c := assembleBook(t, book)
	body := joinBodies(chapters[:1])

	// missing id → warned, patched to ch2's start (fid 1, off 0)
	if len(c.warnings) == 0 || !strings.Contains(c.warnings[0], "missing") {
		t.Errorf("expected a missing-target warning, got %v", c.warnings)
	}
	links := posLinkRe.FindAllStringSubmatch(body, -1)
	if len(links) != 3 { // missing-id link, file link, self "#" link
		t.Fatalf("position links = %d, want 3 in: %s", len(links), body)
	}
	for i, want := range []string{"0001", "0001", "0000"} {
		if links[i][1] != want || links[i][2] != "0000000000" {
			t.Errorf("link %d = fid:%s off:%s, want fid:%s off:0000000000", i, links[i][1], links[i][2], want)
		}
	}
	// non-spine target loses its href but keeps its text
	if strings.Contains(body, "nav.xhtml") {
		t.Errorf("non-spine link should be dropped: %s", body)
	}
	if !strings.Contains(body, "스파인 밖") {
		t.Errorf("dropped link lost its text: %s", body)
	}
}

func TestSVGCoverBecomesImg(t *testing.T) {
	book := &epub.Book{
		Title: "표지", Author: "저자",
		Chapters: []epub.Chapter{{
			Path: "EPUB/titlepage.xhtml",
			HTML: []byte(`<html><body><svg xmlns="http://www.w3.org/2000/svg" id="cover-svg">
				<image xlink:href="cover.png" width="10" height="10"/></svg></body></html>`),
		}},
		Images: map[string][]byte{"EPUB/cover.png": testPNG(t, 10, 10)},
	}
	chapters, _ := assembleBook(t, book)
	body := joinBodies(chapters)
	if !strings.Contains(body, "kindle:embed:0001") {
		t.Errorf("svg image not rewritten: %s", body)
	}
	if strings.Contains(body, "<svg") {
		t.Errorf("svg wrapper should be replaced: %s", body)
	}
	if !strings.Contains(body, `id="cover-svg"`) {
		t.Errorf("svg id should carry over to the img: %s", body)
	}
}

func TestUndecodableImageDropped(t *testing.T) {
	book := &epub.Book{
		Title: "그림", Author: "저자",
		Chapters: []epub.Chapter{{
			Path: "EPUB/ch.xhtml",
			HTML: []byte(`<html><body><p>text</p><img src="bad.webp"/></body></html>`),
		}},
		Images: map[string][]byte{"EPUB/bad.webp": []byte("not an image")},
	}
	chapters, c := assembleBook(t, book)
	body := joinBodies(chapters)
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
