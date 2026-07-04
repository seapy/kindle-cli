package azw3

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestParseBodySelfClosedTitle(t *testing.T) {
	// regression: HTML5 parsing treats <title/> as an open RCDATA tag and
	// swallows the whole document, leaving an empty body (seen in
	// real-world EPUBs)
	doc := `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title/></head>
<body><pre id="x1"/><p>본문 내용</p></body></html>`
	body := parseBody([]byte(doc))
	if body == nil {
		t.Fatal("no body found")
	}
	var rendered strings.Builder
	for n := body.FirstChild; n != nil; n = n.NextSibling {
		rendered.WriteString(renderXHTML(n))
	}
	got := rendered.String()
	if !strings.Contains(got, "<p>본문 내용</p>") {
		t.Errorf("body content lost: %q", got)
	}
	// the self-closed <pre/> must stay empty instead of swallowing the <p>
	if strings.Contains(got, "<pre id=\"x1\">") {
		t.Errorf("pre swallowed following content: %q", got)
	}
}

func TestParseBodyResolvesHTMLEntities(t *testing.T) {
	doc := `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>t</title></head>
<body><p>a&nbsp;b &amp; c</p></body></html>`
	body := parseBody([]byte(doc))
	if body == nil {
		t.Fatal("no body found")
	}
	text := collectText(body)
	if !strings.Contains(text, "a b & c") {
		t.Errorf("entities not resolved: %q", text)
	}
}

func TestParseBodyFallsBackToHTML5(t *testing.T) {
	// not well-formed XML (unclosed tags) — must still find a body
	doc := `<html><body><p>one<p>two</body></html>`
	body := parseBody([]byte(doc))
	if body == nil {
		t.Fatal("no body found via HTML5 fallback")
	}
	if text := collectText(body); !strings.Contains(text, "one") || !strings.Contains(text, "two") {
		t.Errorf("fallback lost content: %q", text)
	}
}

func TestParseBodyWithoutBodyTag(t *testing.T) {
	// no <body> in the source: the HTML5 fallback synthesizes one around the
	// content instead of dropping the chapter
	body := parseBody([]byte(`<notes><item>x</item></notes>`))
	if body == nil {
		t.Fatal("expected synthesized body")
	}
	if text := collectText(body); !strings.Contains(text, "x") {
		t.Errorf("content lost: %q", text)
	}
}

func collectText(n *html.Node) string {
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
	walk(n)
	return sb.String()
}
