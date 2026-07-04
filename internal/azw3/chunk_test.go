package azw3

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// mustChunks lets a two-valued splitNodes call be used inline where only
// the chunk strings matter.
func mustChunks(chunks []string, _ map[string]chunkPos) []string { return chunks }

// bodyNodes parses an HTML fragment and returns the children of <body>.
func bodyNodes(t *testing.T, fragment string) []*html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<html><body>" + fragment + "</body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	body := findElement(doc, atom.Body)
	var nodes []*html.Node
	for n := body.FirstChild; n != nil; n = n.NextSibling {
		nodes = append(nodes, n)
	}
	return nodes
}

// assertWellFormedXML fails when chunk is not parseable as strict XML —
// which is how Kindle firmware reads KF8 parts.
func assertWellFormedXML(t *testing.T, chunk string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader("<root>" + chunk + "</root>"))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("chunk is not well-formed XML: %v\n%s", err, chunk)
		}
	}
}

// visibleText extracts the concatenated text content of a chunk.
func visibleText(t *testing.T, chunk string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(chunk))
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return sb.String()
}

func TestSplitSmallContentIsSingleChunk(t *testing.T) {
	nodes := bodyNodes(t, "<p>hello</p><p>world</p>")
	chunks, _ := splitNodes(nodes, nil, maxChunkBytes, nil)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if chunks[0] != "<p>hello</p><p>world</p>" {
		t.Errorf("chunk = %q", chunks[0])
	}
}

func TestSplitLargeChapter(t *testing.T) {
	// ~100 paragraphs of Korean text, well past one chunk
	var frag strings.Builder
	var wantText strings.Builder
	for i := 0; i < 100; i++ {
		para := fmt.Sprintf("문단 %d — %s", i, strings.Repeat("한국어 본문 텍스트. ", 20))
		frag.WriteString("<p>" + para + "</p>")
		wantText.WriteString(para)
	}
	chunks, _ := splitNodes(bodyNodes(t, frag.String()), nil, maxChunkBytes, nil)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	var gotText strings.Builder
	for _, chunk := range chunks {
		if len(chunk) > maxChunkBytes+200 {
			t.Errorf("chunk exceeds limit: %d bytes", len(chunk))
		}
		assertWellFormedXML(t, chunk)
		gotText.WriteString(visibleText(t, chunk))
	}
	if gotText.String() != wantText.String() {
		t.Error("text content changed across chunk splits")
	}
}

func TestSplitReopensWrappingElement(t *testing.T) {
	// one huge <div> that must be split internally
	var frag strings.Builder
	frag.WriteString(`<div class="chapter">`)
	for i := 0; i < 100; i++ {
		frag.WriteString("<p>" + strings.Repeat("본문. ", 40) + "</p>")
	}
	frag.WriteString("</div>")
	chunks, _ := splitNodes(bodyNodes(t, frag.String()), nil, maxChunkBytes, nil)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		assertWellFormedXML(t, chunk)
		if !strings.HasPrefix(chunk, `<div class="chapter">`) {
			t.Errorf("chunk %d does not reopen the wrapping div: %.60s", i, chunk)
		}
		if !strings.HasSuffix(chunk, "</div>") {
			t.Errorf("chunk %d does not close the wrapping div: …%s", i, chunk[len(chunk)-30:])
		}
	}
}

func TestSplitAppliesWrapperToEveryChunk(t *testing.T) {
	wrapper := &html.Node{
		Type: html.ElementNode, DataAtom: atom.Div, Data: "div",
		Attr: []html.Attribute{{Key: "class", Val: "main"}},
	}
	var frag strings.Builder
	for i := 0; i < 60; i++ {
		frag.WriteString("<p>" + strings.Repeat("텍스트 ", 60) + "</p>")
	}
	chunks, _ := splitNodes(bodyNodes(t, frag.String()), wrapper, maxChunkBytes, nil)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if !strings.HasPrefix(chunk, `<div class="main">`) || !strings.HasSuffix(chunk, "</div>") {
			t.Errorf("chunk %d missing wrapper: %.60s", i, chunk)
		}
		assertWellFormedXML(t, chunk)
	}
}

func TestSplitGiantTextNode(t *testing.T) {
	// a single text run larger than a whole chunk, with entities sprinkled in
	text := strings.Repeat("글자들 & 기호 <표시> 사이. ", 800)
	chunks, _ := splitNodes(bodyNodes(t, "<p>"+html.EscapeString(text)+"</p>"), nil, maxChunkBytes, nil)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	var got strings.Builder
	for _, chunk := range chunks {
		assertWellFormedXML(t, chunk)
		got.WriteString(visibleText(t, chunk))
	}
	if got.String() != text {
		t.Error("giant text node corrupted by splitting")
	}
}

func TestRenderXHTMLVoidElements(t *testing.T) {
	nodes := bodyNodes(t, `<p>a<br>b</p><img src="x.jpg" alt="y">`)
	chunks, _ := splitNodes(nodes, nil, maxChunkBytes, nil)
	got := strings.Join(chunks, "")
	if !strings.Contains(got, "<br/>") {
		t.Errorf("br not self-closed: %s", got)
	}
	if !strings.Contains(got, `<img src="x.jpg" alt="y"/>`) {
		t.Errorf("img not self-closed: %s", got)
	}
	assertWellFormedXML(t, got)
}

func TestRenderXHTMLEscaping(t *testing.T) {
	nodes := bodyNodes(t, `<p title="a&quot;&lt;b">x &amp; y &lt; z</p>`)
	got := strings.Join(mustChunks(splitNodes(nodes, nil, maxChunkBytes, nil)), "")
	assertWellFormedXML(t, got)
	if !strings.Contains(got, "x &amp; y &lt; z") {
		t.Errorf("text not escaped: %s", got)
	}
}

func TestRenderXHTMLDropsNamespacedAttrs(t *testing.T) {
	nodes := bodyNodes(t, `<p epub:type="pagebreak" xml:lang="ko" class="x">t</p>`)
	got := strings.Join(mustChunks(splitNodes(nodes, nil, maxChunkBytes, nil)), "")
	if strings.Contains(got, "epub:type") {
		t.Errorf("undeclared-namespace attr kept: %s", got)
	}
	if !strings.Contains(got, `xml:lang="ko"`) || !strings.Contains(got, `class="x"`) {
		t.Errorf("xml:/plain attrs must survive: %s", got)
	}
}

// assertAnchor checks that a tracked anchor points at the start tag of the
// element carrying it: the chunk body at that offset must open a tag whose
// attributes include the id/name.
func assertAnchor(t *testing.T, chunks []string, anchors map[string]chunkPos, key, wantAttr string) {
	t.Helper()
	pos, ok := anchors[key]
	if !ok {
		t.Fatalf("anchor %q not tracked (got %v)", key, anchors)
	}
	if pos.chunk >= len(chunks) || pos.off >= len(chunks[pos.chunk]) {
		t.Fatalf("anchor %q out of range: %+v with %d chunks", key, pos, len(chunks))
	}
	at := chunks[pos.chunk][pos.off:]
	if !strings.HasPrefix(at, "<") {
		t.Fatalf("anchor %q does not point at a tag: %.60q", key, at)
	}
	tag := at[:strings.IndexByte(at, '>')+1]
	if !strings.Contains(tag, wantAttr) {
		t.Errorf("anchor %q points at %q, want a tag with %q", key, tag, wantAttr)
	}
}

func TestAnchorsTrackedInSingleChunk(t *testing.T) {
	nodes := bodyNodes(t, `<p>intro</p><p id="fn1">note</p><div><span id="deep">x</span></div><a name="legacy">y</a>`)
	wanted := map[string]bool{"fn1": true, "deep": true, "legacy": true}
	chunks, anchors := splitNodes(nodes, nil, maxChunkBytes, wanted)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	assertAnchor(t, chunks, anchors, "fn1", `id="fn1"`)
	assertAnchor(t, chunks, anchors, "deep", `id="deep"`)
	assertAnchor(t, chunks, anchors, "legacy", `name="legacy"`)
}

func TestAnchorsTrackedAcrossSplits(t *testing.T) {
	// a chapter big enough to split, with targets scattered around — one on
	// the huge split element itself, one deep in a late paragraph
	var frag strings.Builder
	frag.WriteString(`<div id="top" class="chapter">`)
	for i := 0; i < 100; i++ {
		if i == 70 {
			frag.WriteString(`<p>본문 <span id="fn70">주석 70</span></p>`)
			continue
		}
		frag.WriteString("<p>" + strings.Repeat("본문. ", 40) + "</p>")
	}
	frag.WriteString("</div>")
	wanted := map[string]bool{"top": true, "fn70": true}
	chunks, anchors := splitNodes(bodyNodes(t, frag.String()), nil, maxChunkBytes, wanted)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	assertAnchor(t, chunks, anchors, "top", `id="top"`)
	assertAnchor(t, chunks, anchors, "fn70", `id="fn70"`)
	if anchors["top"].chunk != 0 || anchors["top"].off != 0 {
		t.Errorf("split element's own anchor must be its first opening, got %+v", anchors["top"])
	}
	if anchors["fn70"].chunk == 0 {
		t.Errorf("late anchor should land in a later chunk, got %+v", anchors["fn70"])
	}
	// the reopened <div id="top"> at each chunk start must not steal the
	// anchor: only chunk 0 offset 0 is the real opening
	for i := 1; i < len(chunks); i++ {
		if !strings.HasPrefix(chunks[i], `<div id="top"`) {
			t.Fatalf("chunk %d should reopen the div: %.60q", i, chunks[i])
		}
	}
}

func TestWriteLargeBookChunksAllChapters(t *testing.T) {
	// end-to-end: a book whose single chapter far exceeds one chunk still
	// produces a valid AZW3 (exercises multi-chunk chapters through the
	// full KF8 serialization)
	var frag bytes.Buffer
	frag.WriteString("<html><body>")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&frag, "<p>문단 %d %s</p>", i, strings.Repeat("내용 ", 50))
	}
	frag.WriteString("</body></html>")

	book := testBook(t)
	book.Chapters[0].HTML = frag.Bytes()
	book.Images["EPUB/image/pic.png"] = testPNG(t, 4, 4)
	book.Images["EPUB/cover.jpg"] = testPNG(t, 100, 150)

	out := t.TempDir() + "/big.azw3"
	if _, err := Write(book, out, Options{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
}
