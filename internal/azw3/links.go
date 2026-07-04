package azw3

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/seapy/kindle-cli/internal/epub"
)

// In-book links in KF8 are position-based: kindle:pos:fid:XXXX:off:YYYYYYYYYY
// names the XXXX-th chunk (base32, across the whole book) and a byte offset
// into that chunk's body. Those coordinates only exist after chunking, so
// links are written in two passes: rewrite() puts a fixed-width placeholder
// in each href, chunking records where every link target lands, and
// linkReplacer() patches the placeholders. Placeholder and final link are
// byte-identical in length, so patching never shifts recorded offsets.
const (
	posFidWidth = 4
	posOffWidth = 10
	// placeholder fid ZZZZ (chunk 1048575) can't collide with a real link,
	// and would be ignored by the firmware if a bug ever left it unpatched
	posPlaceholderPrefix = "kindle:pos:fid:ZZZZ:off:"
)

// linkTarget identifies an in-book link destination.
type linkTarget struct {
	path string // spine document zip path
	id   string // element id or <a name>; "" = top of the document
}

// fidPos is a resolved KF8 position: global chunk index plus byte offset
// into that chunk's body.
type fidPos struct {
	fid int
	off int
}

// to32w renders v in the uppercase base32 used by kindle: URIs, zero-padded
// to width. ok is false when v does not fit.
func to32w(v, width int) (string, bool) {
	s := strings.ToUpper(strconv.FormatInt(int64(v), 32))
	if len(s) > width {
		return "", false
	}
	return strings.Repeat("0", width-len(s)) + s, true
}

func placeholderHref(serial int) string {
	off, ok := to32w(serial, posOffWidth)
	if !ok { // 32^10 targets; unreachable
		panic("azw3: link serial overflow")
	}
	return posPlaceholderPrefix + off
}

func posHref(p fidPos) (string, bool) {
	fid, ok1 := to32w(p.fid, posFidWidth)
	off, ok2 := to32w(p.off, posOffWidth)
	return "kindle:pos:fid:" + fid + ":off:" + off, ok1 && ok2
}

// registerLink allocates (or reuses) the placeholder href for target.
func (c *converter) registerLink(t linkTarget) string {
	serial, ok := c.linkSerials[t]
	if !ok {
		serial = len(c.linkTargets)
		c.linkTargets = append(c.linkTargets, t)
		c.linkSerials[t] = serial
	}
	return placeholderHref(serial)
}

// wantedFor returns the ids that some link targets in the document at path.
func (c *converter) wantedFor(path string) map[string]bool {
	var wanted map[string]bool
	for _, t := range c.linkTargets {
		if t.path == path && t.id != "" {
			if wanted == nil {
				wanted = map[string]bool{}
			}
			wanted[t.id] = true
		}
	}
	return wanted
}

// collectIDs gathers every element id (and legacy <a name>) in the tree.
// It runs before rewrite() mutates anything, so links can be validated
// against the documents as authored.
func collectIDs(n *html.Node, ids map[string]bool) {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if a.Namespace != "" || a.Val == "" {
				continue
			}
			if a.Key == "id" || (a.Key == "name" && n.Data == "a") {
				ids[a.Val] = true
			}
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		collectIDs(child, ids)
	}
}

var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// rewriteAnchor handles an <a> element: external http/https/mailto links are
// kept, in-book links to spine documents become position-link placeholders,
// and everything else (unsupported schemes, non-spine files) loses its href
// but keeps its text.
func (c *converter) rewriteAnchor(n *html.Node, docPath string) {
	for i, a := range n.Attr {
		if a.Key != "href" || a.Namespace != "" {
			continue
		}
		href := strings.TrimSpace(a.Val)
		if m := schemeRe.FindString(href); m != "" {
			switch strings.ToLower(strings.TrimSuffix(m, ":")) {
			case "http", "https", "mailto":
				return // external link: keep as-is
			}
			dropAttr(n, i)
			return
		}
		if href == "" {
			dropAttr(n, i)
			return
		}
		pathPart, frag, _ := strings.Cut(href, "#")
		target := linkTarget{path: docPath}
		if pathPart != "" {
			target.path = epub.ResolveRelative(docPath, pathPart)
		}
		ids, inSpine := c.docIDs[target.path]
		if !inSpine {
			dropAttr(n, i) // nav docs, images, missing files, …
			return
		}
		if frag != "" {
			if f, err := url.PathUnescape(frag); err == nil {
				frag = f
			}
			if ids[frag] {
				target.id = frag
			} else {
				c.warn("link target %s#%s not found; linking to the chapter start", target.path, frag)
			}
		}
		n.Attr[i].Val = c.registerLink(target)
		return
	}
}

func dropAttr(n *html.Node, i int) {
	n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
}

// linkReplacer builds the placeholder→link patch set. anchors holds the
// position of every link target discovered during chunking, plus a
// {path, ""} chapter-start entry per emitted chapter; bookOrder lists the
// parsed chapters' paths in spine order for fallbacks.
func (c *converter) linkReplacer(anchors map[linkTarget]fidPos, bookOrder []string) *strings.Replacer {
	if len(c.linkTargets) == 0 {
		return nil
	}
	orderIdx := make(map[string]int, len(bookOrder))
	for i, p := range bookOrder {
		orderIdx[p] = i
	}
	pairs := make([]string, 0, 2*len(c.linkTargets))
	for serial, t := range c.linkTargets {
		pos, ok := anchors[t]
		if !ok {
			// the id existed in the source but its element did not survive
			// conversion (script, svg, the <body> tag itself, or an empty
			// chapter): degrade to the nearest following chapter start
			if t.id != "" {
				c.warn("link target %s#%s lost in conversion; linking to the chapter start", t.path, t.id)
			}
			pos = chapterStart(t.path, anchors, bookOrder, orderIdx)
		}
		href, fits := posHref(pos)
		if !fits { // >32^4 chunks or >32^10 offset; keep lengths intact regardless
			c.warn("link to %s#%s out of range; linking to the book start", t.path, t.id)
			href, _ = posHref(fidPos{})
		}
		pairs = append(pairs, `"`+placeholderHref(serial)+`"`, `"`+href+`"`)
	}
	return strings.NewReplacer(pairs...)
}

// chapterStart returns the position of the first emitted chapter at or after
// path in spine order, or the book start when none exists.
func chapterStart(path string, anchors map[linkTarget]fidPos, bookOrder []string, orderIdx map[string]int) fidPos {
	for i := orderIdx[path]; i < len(bookOrder); i++ {
		if pos, ok := anchors[linkTarget{path: bookOrder[i]}]; ok {
			return pos
		}
	}
	return fidPos{}
}
