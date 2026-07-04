package azw3

import (
	"strings"

	"golang.org/x/net/html"
)

// maxChunkBytes bounds the content of one KF8 chunk. Each chunk becomes a
// skeleton+fragment pair, and Kindle firmware expects fragments to be small
// (Calibre caps them at 8192 bytes); oversized fragments render as empty
// pages on-device even though the text is present in the file.
const maxChunkBytes = 7600

// chunkPos locates an emitted start tag: the chunk's index within the
// chapter and the byte offset of the tag's '<' inside that chunk's body.
// This is exactly the coordinate a kindle:pos:fid:off link needs, since the
// KF8 fragment table records each chunk's body start as its position.
type chunkPos struct {
	chunk int
	off   int
}

// anchorMark is a link-target start tag found while serializing a subtree,
// at off bytes into the serialized string.
type anchorMark struct {
	key string
	off int
}

// splitNodes serializes nodes (the children of a chapter's <body>) into one
// or more XHTML chunks of at most roughly maxBytes each. Chunks are always
// balanced: when a split lands inside an element, the element is closed at
// the chunk boundary and reopened in the next chunk. wrapper, when non-nil,
// is an element (not serialized as part of the input) that wraps every chunk
// — used to carry the original <body>'s class/style.
//
// wanted names link-target ids (or <a name>s); the returned map holds the
// position of each one's first emitted start tag. Tags reopened at chunk
// boundaries never win: the original emission always comes first.
func splitNodes(nodes []*html.Node, wrapper *html.Node, maxBytes int, wanted map[string]bool) ([]string, map[string]chunkPos) {
	c := &chunker{max: maxBytes, wanted: wanted, anchors: map[string]chunkPos{}}
	if wrapper != nil {
		c.push(wrapper)
	}
	for _, n := range nodes {
		c.emit(n)
	}
	c.flush()
	return c.chunks, c.anchors
}

type chunker struct {
	max    int
	chunks []string
	cur    strings.Builder
	open   []*html.Node // elements reopened at the start of every chunk
	base   int          // cur length right after reopening tags (empty-chunk marker)

	wanted  map[string]bool     // link-target ids to locate
	anchors map[string]chunkPos // id → first emitted position
}

// note records marks — relative to the current write position — for wanted
// ids not yet seen. Call just before writing the string they refer to.
func (c *chunker) note(marks []anchorMark) {
	for _, m := range marks {
		if _, dup := c.anchors[m.key]; !dup {
			c.anchors[m.key] = chunkPos{chunk: len(c.chunks), off: c.cur.Len() + m.off}
		}
	}
}

// push opens an element for the remainder of the traversal.
func (c *chunker) push(n *html.Node) {
	for _, key := range wantedKeys(n, c.wanted) {
		c.note([]anchorMark{{key: key}})
	}
	c.cur.WriteString(startTag(n, false))
	c.open = append(c.open, n)
}

func (c *chunker) pop() {
	n := c.open[len(c.open)-1]
	c.open = c.open[:len(c.open)-1]
	c.cur.WriteString("</" + n.Data + ">")
}

// flush ends the current chunk (closing any open elements) and starts the
// next one (reopening them). A chunk holding nothing but reopened tags is
// discarded rather than emitted.
func (c *chunker) flush() {
	if c.cur.Len() > c.base {
		body := c.cur.String()
		for i := len(c.open) - 1; i >= 0; i-- {
			body += "</" + c.open[i].Data + ">"
		}
		c.chunks = append(c.chunks, body)
	}
	c.cur.Reset()
	for _, n := range c.open {
		c.cur.WriteString(startTag(n, false))
	}
	c.base = c.cur.Len()
}

func (c *chunker) fits(n int) bool {
	return c.cur.Len()+n <= c.max
}

func (c *chunker) emit(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		c.emitText(escapeText(n.Data))
		return
	case html.ElementNode:
	default:
		return // comments, doctypes: dropped
	}

	rendered, marks := renderMarked(n, c.wanted)
	if !c.fits(len(rendered)) && c.cur.Len() > c.base {
		c.flush()
	}
	if c.fits(len(rendered)) {
		c.note(marks)
		c.cur.WriteString(rendered)
		return
	}
	// the element alone exceeds a chunk: descend, splitting between children
	if n.FirstChild == nil || isRawText(n) {
		c.note(marks)
		c.cur.WriteString(rendered) // childless or unsplittable; keep whole
		return
	}
	c.push(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		c.emit(child)
	}
	c.pop()
}

// emitText writes escaped text, splitting it across chunks at rune
// boundaries when a single run of text is itself larger than a chunk.
func (c *chunker) emitText(escaped string) {
	for escaped != "" {
		if c.fits(len(escaped)) {
			c.cur.WriteString(escaped)
			return
		}
		room := c.max - c.cur.Len()
		cut := 0
		for i := range escaped {
			if i > room {
				break
			}
			// never split inside an entity like &amp;
			if j := strings.LastIndexByte(escaped[:i], '&'); j >= 0 && !strings.ContainsRune(escaped[j:i], ';') {
				continue
			}
			cut = i
		}
		if cut == 0 { // no room at all in this chunk
			if c.cur.Len() == c.base {
				cut = len(escaped) // degenerate max; avoid an infinite loop
			}
		}
		c.cur.WriteString(escaped[:cut])
		escaped = escaped[cut:]
		if escaped != "" {
			c.flush()
		}
	}
}

// renderXHTML serializes a node as well-formed XHTML. KF8 parts are XML
// documents (the skeleton carries an <?xml?> declaration), and Kindle
// firmware is strict about it — HTML5-style void tags like <br> make the
// whole part render empty.
func renderXHTML(n *html.Node) string {
	var sb strings.Builder
	writeXHTML(&sb, n, nil, nil)
	return sb.String()
}

// renderMarked is renderXHTML plus the positions of wanted link targets.
func renderMarked(n *html.Node, wanted map[string]bool) (string, []anchorMark) {
	var sb strings.Builder
	var marks []anchorMark
	writeXHTML(&sb, n, wanted, &marks)
	return sb.String(), marks
}

func writeXHTML(sb *strings.Builder, n *html.Node, wanted map[string]bool, marks *[]anchorMark) {
	switch n.Type {
	case html.TextNode:
		if isRawText(n.Parent) {
			sb.WriteString(n.Data)
		} else {
			sb.WriteString(escapeText(n.Data))
		}
	case html.ElementNode:
		for _, key := range wantedKeys(n, wanted) {
			*marks = append(*marks, anchorMark{key: key, off: sb.Len()})
		}
		if n.FirstChild == nil {
			sb.WriteString(startTag(n, true))
			return
		}
		sb.WriteString(startTag(n, false))
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			writeXHTML(sb, child, wanted, marks)
		}
		sb.WriteString("</" + n.Data + ">")
	}
}

// wantedKeys returns the entries of wanted this element satisfies, via its
// id or — for <a> elements, as in older EPUBs — its name attribute.
func wantedKeys(n *html.Node, wanted map[string]bool) []string {
	if len(wanted) == 0 || n.Type != html.ElementNode {
		return nil
	}
	var keys []string
	for _, a := range n.Attr {
		if a.Namespace != "" || a.Val == "" {
			continue
		}
		if a.Key == "id" || (a.Key == "name" && n.Data == "a") {
			if wanted[a.Val] {
				keys = append(keys, a.Val)
			}
		}
	}
	return keys
}

func isRawText(n *html.Node) bool {
	return n != nil && n.Type == html.ElementNode && n.Data == "style"
}

func startTag(n *html.Node, selfClose bool) string {
	var sb strings.Builder
	sb.WriteString("<" + n.Data)
	for _, a := range n.Attr {
		key := a.Key
		if a.Namespace != "" {
			key = a.Namespace + ":" + key
		}
		// namespaced attributes (epub:type, xlink:…) would need xmlns
		// declarations the skeleton doesn't have; drop them for XML validity
		if strings.Contains(key, ":") && !strings.HasPrefix(key, "xml:") {
			continue
		}
		sb.WriteString(" " + key + `="` + escapeAttr(a.Val) + `"`)
	}
	if selfClose {
		sb.WriteString("/>")
	} else {
		sb.WriteString(">")
	}
	return sb.String()
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
)

func escapeText(s string) string { return textEscaper.Replace(s) }
func escapeAttr(s string) string { return attrEscaper.Replace(s) }
