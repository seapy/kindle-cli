package azw3

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seapy/kindle-cli/internal/epub"
)

func encodeImage(t *testing.T, encode func(*bytes.Buffer, image.Image) error, alpha uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{200, uint8(x * 30), uint8(y * 30), alpha})
		}
	}
	var buf bytes.Buffer
	if err := encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testJPEG(t *testing.T) []byte {
	return encodeImage(t, func(b *bytes.Buffer, i image.Image) error {
		return jpeg.Encode(b, i, nil)
	}, 255)
}

func testGIF(t *testing.T) []byte {
	return encodeImage(t, func(b *bytes.Buffer, i image.Image) error {
		return gif.Encode(b, i, nil)
	}, 255)
}

// transparentPNG has a real alpha channel, which pass-through must preserve.
func transparentPNG(t *testing.T) []byte {
	return encodeImage(t, func(b *bytes.Buffer, i image.Image) error {
		return png.Encode(b, i)
	}, 128)
}

// sofJPEG builds the marker skeleton of a JPEG up to its SOF segment.
func sofJPEG(sofMarker byte, nComponents int) []byte {
	segLen := 8 + 3*nComponents
	data := []byte{0xFF, 0xD8, 0xFF, sofMarker, byte(segLen >> 8), byte(segLen & 0xFF),
		8, 0, 16, 0, 16, byte(nComponents)}
	return append(data, make([]byte, 3*nComponents)...)
}

func TestClassifyImage(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		mime     string
		passthru bool
	}{
		{"png", testPNG(t, 4, 4), "image/png", true},
		{"gif", testGIF(t), "image/gif", true},
		{"baseline jpeg", testJPEG(t), "image/jpeg", true},
		{"progressive jpeg", sofJPEG(0xC2, 3), "image/jpeg", false},
		{"cmyk jpeg", sofJPEG(0xC0, 4), "image/jpeg", false},
		{"extended sequential jpeg", sofJPEG(0xC1, 3), "image/jpeg", true},
		{"truncated jpeg", []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00}, "image/jpeg", false},
		{"garbage", []byte("not an image"), "", false},
	}
	for _, tc := range cases {
		mime, pass := classifyImage(tc.data)
		if mime != tc.mime || pass != tc.passthru {
			t.Errorf("%s: classify = (%q, %v), want (%q, %v)", tc.name, mime, pass, tc.mime, tc.passthru)
		}
	}
}

func TestImagesPassThroughVerbatim(t *testing.T) {
	pngData := transparentPNG(t)
	gifData := testGIF(t)
	jpegData := testJPEG(t)

	book := &epub.Book{
		Title: "그림책", Author: "저자",
		Chapters: []epub.Chapter{{
			Path: "EPUB/ch.xhtml",
			HTML: []byte(`<html><body><p>본문</p>
				<img src="a.png"/><img src="b.gif"/><img src="c.jpg"/>
			</body></html>`),
		}},
		Images: map[string][]byte{
			"EPUB/a.png": pngData,
			"EPUB/b.gif": gifData,
			"EPUB/c.jpg": jpegData,
		},
	}

	out := filepath.Join(t.TempDir(), "img.azw3")
	warnings, err := Write(book, out, Options{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "cover") { // no cover in this book: expected
			t.Errorf("unexpected warning: %v", w)
		}
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for name, orig := range map[string][]byte{"png": pngData, "gif": gifData, "jpeg": jpegData} {
		if !bytes.Contains(data, orig) {
			t.Errorf("%s original bytes not embedded verbatim", name)
		}
	}

	// the embed URIs must carry the original media types
	chapters, _ := assembleBook(t, book)
	body := joinBodies(chapters)
	for _, want := range []string{"?mime=image/png", "?mime=image/gif", "?mime=image/jpeg"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in rewritten body: %s", want, body)
		}
	}
}
