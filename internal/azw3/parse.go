package azw3

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// parseBody returns the <body> element of an EPUB content document.
//
// EPUB chapters are XHTML, so an XML parse is attempted first: HTML5
// parsing rules mis-read XML idioms — most fatally <title/>, which as HTML5
// RCDATA swallows the entire rest of the document as title text, leaving an
// empty body. Documents that are not well-formed XML fall back to the
// lenient HTML5 parser. Returns nil when no body can be found either way.
func parseBody(data []byte) *html.Node {
	if root := parseXML(data); root != nil {
		if body := findElement(root, atom.Body); body != nil {
			return body
		}
	}
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return findElement(doc, atom.Body)
}

// parseXML builds an html.Node tree from well-formed XML. Returns nil on any
// parse error.
func parseXML(data []byte) *html.Node {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Entity = xml.HTMLEntity

	root := &html.Node{Type: html.DocumentNode}
	cur := root
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			n := &html.Node{
				Type:     html.ElementNode,
				Data:     name,
				DataAtom: atom.Lookup([]byte(name)),
			}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				n.Attr = append(n.Attr, html.Attribute{Key: attrKey(a.Name), Val: a.Value})
			}
			cur.AppendChild(n)
			cur = n
		case xml.EndElement:
			if cur.Parent == nil {
				return nil
			}
			cur = cur.Parent
		case xml.CharData:
			if cur == root {
				continue // whitespace between top-level constructs
			}
			cur.AppendChild(&html.Node{Type: html.TextNode, Data: string(t)})
		}
		// comments, directives, processing instructions: dropped
	}
	if cur != root {
		return nil
	}
	return root
}

// attrKey flattens an XML attribute name to the html.Attribute key
// convention used by the rest of the pipeline.
func attrKey(name xml.Name) string {
	space := name.Space
	local := strings.ToLower(name.Local)
	switch {
	case space == "":
		return local
	case space == "xml":
		return "xml:" + local
	case space == "xlink" || strings.Contains(space, "xlink"):
		return "xlink:" + local
	default:
		// foreign attribute (epub:type, …); keep the prefix so the XHTML
		// serializer recognizes and drops it
		return space + ":" + local
	}
}
