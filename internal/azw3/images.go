package azw3

import (
	"bytes"
	"fmt"
	"image"

	"github.com/leotaku/mobi/pdb"
	"github.com/leotaku/mobi/records"
)

// imageAsset is one KF8 image resource. Device-safe originals (JPEG/PNG/GIF)
// are kept verbatim in raw — no generation loss, transparency preserved —
// and swapped into the image records after the KF8 writer lays them out.
// Everything else is decoded and re-encoded as JPEG by the writer.
type imageAsset struct {
	img  image.Image // decoded form; nil when raw is used
	raw  []byte      // original bytes passed through unchanged
	mime string
}

// placeholderImage stands in for pass-through images in mobi.Book.Images so
// record counts and indices stay correct; the records it would produce are
// replaced with the original bytes before anything is written.
var placeholderImage = image.NewGray(image.Rect(0, 0, 1, 1))

// loadAsset reads and classifies the image at zipPath. ok is false when the
// image is missing or undecodable (a warning is recorded).
func (c *converter) loadAsset(zipPath string) (imageAsset, bool) {
	data, ok := c.book.Images[zipPath]
	if !ok {
		c.warn("image %s referenced but missing from EPUB", zipPath)
		return imageAsset{}, false
	}
	if mime, pass := classifyImage(data); pass {
		// header-only sanity check so corrupt files are still dropped
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			return imageAsset{raw: data, mime: mime}, true
		}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		c.warn("image %s skipped (%v)", zipPath, err)
		return imageAsset{}, false
	}
	// JPEG re-encode has no alpha channel; composite onto white
	return imageAsset{img: flattenAlpha(img), mime: "image/jpeg"}, true
}

// libraryImages returns the mobi.Book.Images slice matching c.assets.
func (c *converter) libraryImages() []image.Image {
	if len(c.assets) == 0 {
		return nil
	}
	imgs := make([]image.Image, len(c.assets))
	for i, a := range c.assets {
		if a.raw != nil {
			imgs[i] = placeholderImage
		} else {
			imgs[i] = a.img
		}
	}
	return imgs
}

// replaceImageRecords swaps the writer's placeholder image records for the
// pass-through originals. The KF8 writer appends one ImageRecord per entry
// of mobi.Book.Images (then cover and thumbnail), in order, so the n-th
// ImageRecord in the database corresponds to assets[n].
func replaceImageRecords(db *pdb.Database, assets []imageAsset) error {
	n := 0
	for i, rec := range db.Records {
		if _, ok := rec.(records.ImageRecord); !ok {
			continue
		}
		if n < len(assets) && assets[n].raw != nil {
			db.Records[i] = pdb.RawRecord(assets[n].raw)
		}
		n++
	}
	if n < len(assets) {
		return fmt.Errorf("KF8 writer image record layout changed: found %d records for %d images", n, len(assets))
	}
	return nil
}

// classifyImage reports the media type of data and whether it can be
// embedded as-is. PNG and GIF are device-safe (Kindle renders both,
// transparency included). JPEG originals must be baseline and non-CMYK:
// progressive scans and CMYK color are valid JPEG that Kindle firmware
// does not render, so those fall back to re-encoding.
func classifyImage(data []byte) (mime string, passthrough bool) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", true
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", true
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8}):
		return "image/jpeg", jpegKindleSafe(data)
	}
	return "", false
}

// jpegKindleSafe scans JPEG segment markers until the first SOF and accepts
// only baseline or extended-sequential Huffman frames (SOF0/SOF1) with at
// most 3 color components.
func jpegKindleSafe(data []byte) bool {
	i := 2
	for i+2 <= len(data) {
		if data[i] != 0xFF {
			return false
		}
		marker := data[i+1]
		switch {
		case marker == 0xFF: // fill byte
			i++
			continue
		case marker == 0x01, marker >= 0xD0 && marker <= 0xD7: // standalone
			i += 2
			continue
		case marker == 0xD9, marker == 0xDA: // EOI/SOS before any SOF
			return false
		}
		if i+4 > len(data) {
			return false
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if segLen < 2 || i+2+segLen > len(data) {
			return false
		}
		isSOF := marker >= 0xC0 && marker <= 0xCF &&
			marker != 0xC4 && marker != 0xC8 && marker != 0xCC
		if isSOF {
			if marker != 0xC0 && marker != 0xC1 {
				return false // progressive, arithmetic, lossless, …
			}
			if segLen < 8 || i+10 > len(data) {
				return false
			}
			nComponents := data[i+9]
			return nComponents <= 3
		}
		i += 2 + segLen
	}
	return false
}
