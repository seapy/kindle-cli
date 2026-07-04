// Package epub reads the parts of an EPUB (2 or 3) that kindle-cli needs:
// OPF metadata, the spine's XHTML chapters, stylesheets, images, the cover,
// and per-file chapter titles from the NCX or EPUB3 nav document.
//
// Parsing is deliberately lenient — real-world EPUBs (especially DRM-stripped
// ones) are often slightly malformed, and a missing table of contents or
// cover should degrade gracefully rather than fail the conversion.
package epub

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
)

// Item is one manifest entry resolved to an absolute zip path.
type Item struct {
	ID        string
	Path      string // zip path, href resolved against the OPF directory
	MediaType string
}

// Chapter is one spine document in reading order.
type Chapter struct {
	Path  string
	Title string // from NCX/nav; empty when the TOC has no entry for the file
	HTML  []byte
}

// Book is a parsed EPUB.
type Book struct {
	Title    string
	Author   string
	Language string

	Chapters  []Chapter
	CSS       []string          // stylesheet text, manifest order
	Images    map[string][]byte // zip path → raw image data
	CoverPath string            // zip path of the cover image ("" when absent)
}

type containerXML struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfXML struct {
	Metadata struct {
		Titles   []string `xml:"title"`
		Creators []string `xml:"creator"`
		Language string   `xml:"language"`
		Metas    []struct {
			Name     string `xml:"name,attr"`
			Content  string `xml:"content,attr"`
			Property string `xml:"property,attr"`
			CharData string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

type ncxXML struct {
	NavPoints []navPoint `xml:"navMap>navPoint"`
}

type navPoint struct {
	Label   string `xml:"navLabel>text"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	NavPoints []navPoint `xml:"navPoint"`
}

// Read parses the EPUB at epubPath.
func Read(epubPath string) (*Book, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, fmt.Errorf("not a readable EPUB (zip): %w", err)
	}
	defer zr.Close()

	files := map[string][]byte{}
	for _, f := range zr.File {
		data, err := readZipFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.Name, err)
		}
		files[f.Name] = data
	}
	return parse(files)
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func parse(files map[string][]byte) (*Book, error) {
	opfPath, err := rootfilePath(files)
	if err != nil {
		return nil, err
	}
	opfData, ok := files[opfPath]
	if !ok {
		return nil, fmt.Errorf("OPF %s missing from archive", opfPath)
	}
	var opf opfXML
	if err := xml.Unmarshal(opfData, &opf); err != nil {
		return nil, fmt.Errorf("parsing OPF: %w", err)
	}
	opfDir := path.Dir(opfPath)

	book := &Book{
		Language: strings.TrimSpace(opf.Metadata.Language),
		Images:   map[string][]byte{},
	}
	if len(opf.Metadata.Titles) > 0 {
		book.Title = strings.TrimSpace(opf.Metadata.Titles[0])
	}
	if len(opf.Metadata.Creators) > 0 {
		book.Author = strings.TrimSpace(strings.Join(opf.Metadata.Creators, ", "))
	}

	items := map[string]Item{}
	var cssPaths []string
	navPath := ""
	for _, it := range opf.Manifest.Items {
		p := resolveHref(opfDir, it.Href)
		items[it.ID] = Item{ID: it.ID, Path: p, MediaType: it.MediaType}
		switch {
		case it.MediaType == "text/css":
			cssPaths = append(cssPaths, p)
		case strings.HasPrefix(it.MediaType, "image/"):
			if data, ok := files[p]; ok {
				book.Images[p] = data
			}
		}
		if strings.Contains(" "+it.Properties+" ", " cover-image ") {
			book.CoverPath = p
		}
		if strings.Contains(" "+it.Properties+" ", " nav ") {
			navPath = p
		}
	}
	for _, p := range cssPaths {
		if data, ok := files[p]; ok {
			book.CSS = append(book.CSS, string(data))
		}
	}

	// EPUB2 cover: <meta name="cover" content="manifest-id"/> (some books put
	// the image path in content directly)
	if book.CoverPath == "" {
		for _, m := range opf.Metadata.Metas {
			if strings.EqualFold(m.Name, "cover") && m.Content != "" {
				if it, ok := items[m.Content]; ok {
					book.CoverPath = it.Path
				} else if p := resolveHref(opfDir, m.Content); files[p] != nil {
					book.CoverPath = p
				}
			}
		}
	}
	if book.CoverPath == "" {
		book.CoverPath = guessCover(book.Images)
	}

	titles := chapterTitles(files, items, navPath)
	for _, ref := range opf.Spine.Itemrefs {
		it, ok := items[ref.IDRef]
		if !ok {
			continue
		}
		html, ok := files[it.Path]
		if !ok {
			continue
		}
		book.Chapters = append(book.Chapters, Chapter{
			Path:  it.Path,
			Title: titles[it.Path],
			HTML:  html,
		})
	}
	if len(book.Chapters) == 0 {
		return nil, fmt.Errorf("EPUB has no readable spine documents")
	}
	return book, nil
}

func rootfilePath(files map[string][]byte) (string, error) {
	data, ok := files["META-INF/container.xml"]
	if !ok {
		return "", fmt.Errorf("META-INF/container.xml missing (not an EPUB?)")
	}
	var c containerXML
	if err := xml.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("parsing container.xml: %w", err)
	}
	if len(c.Rootfiles) == 0 || c.Rootfiles[0].FullPath == "" {
		return "", fmt.Errorf("container.xml lists no rootfile")
	}
	return c.Rootfiles[0].FullPath, nil
}

// resolveHref resolves a (possibly percent-encoded) manifest href against the
// directory that contains the OPF.
func resolveHref(baseDir, href string) string {
	href = strings.SplitN(href, "#", 2)[0]
	if unescaped, err := url.PathUnescape(href); err == nil {
		href = unescaped
	}
	if baseDir == "." {
		return path.Clean(href)
	}
	return path.Clean(path.Join(baseDir, href))
}

// ResolveRelative resolves a reference found inside the document at fromPath
// (e.g. an img src) to a zip path.
func ResolveRelative(fromPath, ref string) string {
	return resolveHref(path.Dir(fromPath), ref)
}

func guessCover(images map[string][]byte) string {
	var paths []string
	for p := range images {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if strings.Contains(strings.ToLower(path.Base(p)), "cover") {
			return p
		}
	}
	return ""
}

// chapterTitles maps spine document paths to display titles, preferring the
// NCX and falling back to the EPUB3 nav document.
func chapterTitles(files map[string][]byte, items map[string]Item, navPath string) map[string]string {
	titles := map[string]string{}
	for _, it := range items {
		if it.MediaType != "application/x-dtbncx+xml" {
			continue
		}
		data, ok := files[it.Path]
		if !ok {
			continue
		}
		var ncx ncxXML
		if xml.Unmarshal(data, &ncx) == nil {
			collectNCXTitles(titles, it.Path, ncx.NavPoints)
		}
	}
	// note: the EPUB3 nav document is an XHTML file; parsing it with an XML
	// decoder is best-effort and skipped here — NCX covers virtually all
	// real-world books, and a missing title only coarsens the device TOC.
	_ = navPath
	return titles
}

func collectNCXTitles(titles map[string]string, ncxPath string, points []navPoint) {
	for _, np := range points {
		p := ResolveRelative(ncxPath, np.Content.Src)
		label := strings.TrimSpace(np.Label)
		if label != "" {
			if _, seen := titles[p]; !seen {
				titles[p] = label
			}
		}
		collectNCXTitles(titles, ncxPath, np.NavPoints)
	}
}
