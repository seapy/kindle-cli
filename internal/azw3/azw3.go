// Package azw3 renders a parsed EPUB into a KF8/AZW3 book using
// github.com/leotaku/mobi — no Calibre, no external tools.
//
// The EPUB's spine documents become KF8 chapters: each document's body
// content is passed through nearly verbatim (KF8 is HTML+CSS underneath),
// with resource references rewritten to the kindle:embed / kindle:flow
// scheme, re-serialized as strict XHTML, and split into small balanced
// chunks (Kindle renders oversized or non-XML fragments as empty pages).
// The cdetype (EXTH 501) is written at generation time, so no post-patching
// is needed.
//
// In-book hyperlinks (footnote jumps, cross-references) become real KF8
// position links: hrefs are written as fixed-width placeholders, chunking
// records where every link target lands, and the placeholders are patched
// to kindle:pos:fid:off form (see links.go). JPEG/PNG/GIF images are
// embedded byte-for-byte — no re-encoding, transparency preserved; only
// device-unsafe variants (progressive/CMYK JPEG, other formats) are
// re-encoded as JPEG (see images.go).
//
// Known simplifications, chosen to keep sideloaded reflowable books working
// well rather than to cover every EPUB feature:
//   - embedded fonts are dropped (@font-face is stripped; the device renders
//     with its built-in fonts, which cover CJK)
package azw3

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/leotaku/mobi"
	"github.com/leotaku/mobi/records"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/language"

	"github.com/seapy/kindle-cli/internal/epub"
)

// Options control the generated book's metadata.
type Options struct {
	Title   string
	Author  string
	DocType string // "PDOC" (default) or "EBOK"
}

const thumbMaxHeight = 480

// skeletonTemplateString is the KF8 skeleton for each chunk. Unlike the
// library's default template, it ends right after the aid-carrying <body>
// tag: the library records each fragment's insert position as "end of
// skeleton", and Kindle firmware inserts the fragment there when rendering.
// With a full-document skeleton that lands after </html>, where the renderer
// silently discards it — the book shows empty pages. Ending the skeleton at
// <body …> puts the insert position inside the body (exactly where
// KindleUnpack's corrupt-table repair recomputes it), and each chunk carries
// its own closing </body></html>.
const skeletonTemplateString = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head>
    <title>{{ .Mobi.Title }}</title>
    <meta http-equiv="Content-Type" content="text/html; charset=utf-8"/>
    {{- range $i, $_ := .Mobi.CSSFlows }}
    <link rel="stylesheet" type="text/css" href="kindle:flow:{{ $i | inc | base32 }}?mime=text/css"/>
    {{- end }}
  </head>
  <body aid="{{ .Chunk.ID | base32 }}">`

const chunkClose = "</body></html>"

var skeletonTemplate = template.Must(template.New("skeleton").Funcs(template.FuncMap{
	"inc":    func(i int) int { return i + 1 },
	"base32": func(i int) string { return records.To32(i) },
}).Parse(skeletonTemplateString))

// Write renders book to an AZW3 file at outPath. It returns non-fatal
// warnings (skipped images, missing cover, …).
func Write(book *epub.Book, outPath string, opts Options) ([]string, error) {
	c := newConverter(book)

	chapters, err := c.assemble()
	if err != nil {
		return c.warnings, err
	}

	title := opts.Title
	if title == "" {
		title = book.Title
	}
	author := opts.Author
	if author == "" {
		author = book.Author
	}
	docType := opts.DocType
	if docType == "" {
		docType = "PDOC"
	}

	mb := mobi.Book{
		Title:       title,
		Authors:     splitAuthors(author),
		CreatedDate: time.Now(),
		DocType:     docType,
		Language:    parseLanguage(book.Language),
		Chapters:    chapters,
		CSSFlows:    []string{stripFontFaces(strings.Join(book.CSS, "\n"))},
		Images:      c.libraryImages(),
		// deterministic ID: the fake ASIN derived from it stays stable across
		// re-conversions, so replacing a book on-device works cleanly
		UniqueID: crc32.ChecksumIEEE([]byte(title + "\x00" + author)),
	}
	mb = mb.OverrideTemplate(*skeletonTemplate)

	if book.CoverPath != "" {
		if cover := c.decode(book.CoverPath); cover != nil {
			mb.CoverImage = cover
			mb.ThumbImage = scaleToHeight(cover, thumbMaxHeight)
		}
	}
	if mb.CoverImage == nil {
		c.warn("no usable cover image found — the device will show a generic cover")
	}

	f, err := os.Create(outPath)
	if err != nil {
		return c.warnings, err
	}
	defer f.Close()
	if err := writeRealized(mb, c.assets, f); err != nil {
		return c.warnings, fmt.Errorf("writing AZW3: %w", err)
	}
	return c.warnings, nil
}

// writeRealized isolates the library call (mobi.Book.Realize panics on
// malformed input instead of returning an error) and swaps pass-through
// image originals in for the placeholder records before writing.
func writeRealized(mb mobi.Book, assets []imageAsset, f *os.File) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("KF8 serialization failed: %v", r)
		}
	}()
	db := mb.Realize()
	if err := replaceImageRecords(&db, assets); err != nil {
		return err
	}
	return db.Write(f)
}

type converter struct {
	book       *epub.Book
	assets     []imageAsset
	imageIndex map[string]int // zip path → 1-based KF8 resource index
	warnings   []string

	docIDs      map[string]map[string]bool // doc zip path → ids present, pre-rewrite
	linkSerials map[linkTarget]int         // target → placeholder serial
	linkTargets []linkTarget               // serial → target
}

func newConverter(book *epub.Book) *converter {
	return &converter{
		book:        book,
		imageIndex:  map[string]int{},
		linkSerials: map[linkTarget]int{},
	}
}

func (c *converter) warn(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

// assemble runs the EPUB→KF8 chapter pipeline. All documents are parsed and
// rewritten before any is chunked, because rewriting doc A can register link
// targets that chunking doc B must locate; chunking then yields the global
// chunk numbering that position links encode, and a final same-length patch
// turns the placeholder hrefs into kindle:pos links.
func (c *converter) assemble() ([]mobi.Chapter, error) {
	type parsedChapter struct {
		src  epub.Chapter
		idx  int // spine index, for untitled-chapter numbering
		body *html.Node
	}
	var parsed []parsedChapter
	for i, ch := range c.book.Chapters {
		if body := parseBody(ch.HTML); body != nil {
			parsed = append(parsed, parsedChapter{src: ch, idx: i, body: body})
		}
	}

	// link-target inventory, before rewriting mutates the trees
	c.docIDs = make(map[string]map[string]bool, len(parsed))
	for _, pc := range parsed {
		ids := map[string]bool{}
		collectIDs(pc.body, ids)
		c.docIDs[pc.src.Path] = ids
	}
	for _, pc := range parsed {
		c.rewrite(pc.body, pc.src.Path)
	}

	type builtChapter struct {
		title  string
		chunks []string
	}
	var built []builtChapter
	anchors := map[linkTarget]fidPos{}
	bookOrder := make([]string, 0, len(parsed))
	nextFid := 0
	for _, pc := range parsed {
		bookOrder = append(bookOrder, pc.src.Path)
		chunks, anc := c.chunkChapter(pc.body, pc.src.Path)
		if len(chunks) == 0 {
			continue
		}
		anchors[linkTarget{path: pc.src.Path}] = fidPos{fid: nextFid}
		for key, pos := range anc {
			anchors[linkTarget{path: pc.src.Path, id: key}] = fidPos{fid: nextFid + pos.chunk, off: pos.off}
		}
		built = append(built, builtChapter{title: chapterTitle(pc.src, pc.idx), chunks: chunks})
		nextFid += len(chunks)
	}
	if len(built) == 0 {
		return nil, fmt.Errorf("no chapters with content")
	}

	replacer := c.linkReplacer(anchors, bookOrder)
	var chapters []mobi.Chapter
	for _, b := range built {
		chunks := make([]mobi.Chunk, len(b.chunks))
		for i, chunk := range b.chunks {
			if replacer != nil {
				chunk = replacer.Replace(chunk)
			}
			// the skeleton ends open at <body aid="…">
			chunks[i] = mobi.Chunk{Body: chunk + chunkClose}
		}
		chapters = append(chapters, mobi.Chapter{Title: b.title, Chunks: chunks})
	}
	return chapters, nil
}

// chunkChapter serializes a rewritten <body> as one or more well-formed
// XHTML chunks, tracking where this document's link targets land.
func (c *converter) chunkChapter(body *html.Node, path string) ([]string, map[string]chunkPos) {
	var nodes []*html.Node
	hasContent := false
	for n := body.FirstChild; n != nil; n = n.NextSibling {
		nodes = append(nodes, n)
		if n.Type == html.ElementNode || (n.Type == html.TextNode && strings.TrimSpace(n.Data) != "") {
			hasContent = true
		}
	}
	if !hasContent {
		return nil, nil
	}

	// the KF8 skeleton provides its own <body>; carry the original body's
	// class/style over on a per-chunk wrapper so stylesheet selectors still
	// apply
	var wrapper *html.Node
	var kept []html.Attribute
	for _, a := range body.Attr {
		if (a.Key == "class" || a.Key == "style") && a.Val != "" {
			kept = append(kept, html.Attribute{Key: a.Key, Val: a.Val})
		}
	}
	if len(kept) > 0 {
		wrapper = &html.Node{
			Type:     html.ElementNode,
			DataAtom: atom.Div,
			Data:     "div",
			Attr:     kept,
		}
	}
	return splitNodes(nodes, wrapper, maxChunkBytes, c.wantedFor(path))
}

// rewrite walks the tree rewriting resource references to kindle: URIs,
// turning in-book links into position-link placeholders (links.go), and
// dropping scripts.
func (c *converter) rewrite(n *html.Node, docPath string) {
	var next *html.Node
	for child := n.FirstChild; child != nil; child = next {
		next = child.NextSibling
		if child.Type != html.ElementNode {
			continue
		}
		switch {
		case child.DataAtom == atom.Script:
			n.RemoveChild(child)
			continue
		case child.DataAtom == atom.Img:
			c.rewriteImg(child, docPath)
		case child.DataAtom == atom.Svg || child.Data == "svg":
			if img := c.svgToImg(child, docPath); img != nil {
				n.InsertBefore(img, child)
				n.RemoveChild(child)
				continue
			}
		case child.DataAtom == atom.A:
			c.rewriteAnchor(child, docPath)
		}
		c.rewrite(child, docPath)
	}
}

func (c *converter) rewriteImg(n *html.Node, docPath string) {
	for i, a := range n.Attr {
		if a.Key != "src" || a.Namespace != "" {
			continue
		}
		if ref := c.embedRef(docPath, a.Val); ref != "" {
			n.Attr[i].Val = ref
		} else {
			// undecodable image: keep alt text, drop the broken reference
			n.Attr[i].Val = ""
		}
	}
}

// svgToImg converts the common "SVG-wrapped cover" pattern
// (<svg><image xlink:href="…"/></svg>) into a plain <img>, keeping the
// svg's id so links targeting it still resolve.
func (c *converter) svgToImg(svg *html.Node, docPath string) *html.Node {
	imageNode := findByTag(svg, "image")
	if imageNode == nil {
		return nil
	}
	for _, a := range imageNode.Attr {
		if a.Key != "href" && a.Key != "xlink:href" {
			continue
		}
		if ref := c.embedRef(docPath, a.Val); ref != "" {
			attrs := []html.Attribute{{Key: "src", Val: ref}}
			for _, sa := range svg.Attr {
				if sa.Namespace == "" && sa.Key == "id" && sa.Val != "" {
					attrs = append(attrs, html.Attribute{Key: "id", Val: sa.Val})
					break
				}
			}
			return &html.Node{
				Type:     html.ElementNode,
				DataAtom: atom.Img,
				Data:     "img",
				Attr:     attrs,
			}
		}
	}
	return nil
}

// embedRef returns the kindle:embed URI for an image reference, registering
// (and classifying) the image on first use. Empty when the image is unusable.
func (c *converter) embedRef(docPath, ref string) string {
	p := epub.ResolveRelative(docPath, ref)
	idx, ok := c.imageIndex[p]
	if !ok {
		asset, usable := c.loadAsset(p)
		if !usable {
			return ""
		}
		c.assets = append(c.assets, asset)
		idx = len(c.assets)
		c.imageIndex[p] = idx
	}
	return fmt.Sprintf("kindle:embed:%v?mime=%s", records.To32(idx), c.assets[idx-1].mime)
}

// decode reads an image from the EPUB for the cover/thumbnail path, which
// stays on the decode-and-re-encode route (the thumbnail is scaled anyway),
// flattening transparency onto white (JPEG has no alpha).
func (c *converter) decode(zipPath string) image.Image {
	data, ok := c.book.Images[zipPath]
	if !ok {
		c.warn("image %s referenced but missing from EPUB", zipPath)
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		c.warn("image %s skipped (%v)", zipPath, err)
		return nil
	}
	return flattenAlpha(img)
}

func flattenAlpha(img image.Image) image.Image {
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return img
	}
	flat := image.NewRGBA(img.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)
	return flat
}

func scaleToHeight(img image.Image, maxHeight int) image.Image {
	b := img.Bounds()
	if b.Dy() <= maxHeight {
		return img
	}
	w := b.Dx() * maxHeight / b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, maxHeight))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

var fontFaceRe = regexp.MustCompile(`(?s)@font-face\s*\{[^}]*\}`)

func stripFontFaces(css string) string {
	return fontFaceRe.ReplaceAllString(css, "")
}

func parseLanguage(lang string) language.Tag {
	if lang != "" {
		if tag, err := language.Parse(lang); err == nil && tag != language.Und {
			return tag
		}
	}
	return language.English
}

func splitAuthors(author string) []string {
	if author == "" {
		return nil
	}
	parts := strings.Split(author, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func chapterTitle(ch epub.Chapter, index int) string {
	if ch.Title != "" {
		return ch.Title
	}
	if t := htmlTitle(ch.HTML); t != "" {
		return t
	}
	return fmt.Sprintf("— %d —", index+1)
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func htmlTitle(doc []byte) string {
	m := titleRe.FindSubmatch(doc)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(string(m[1])))
}

func findElement(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, a); found != nil {
			return found
		}
	}
	return nil
}

func findByTag(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findByTag(child, tag); found != nil {
			return found
		}
	}
	return nil
}
