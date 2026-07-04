# kindle-cli

**Sideload EPUBs onto modern Kindles (Colorsoft, Scribe 2024, …) with covers that actually show.**

Copy an EPUB to a 2024-era Kindle over USB and one of two things happens: the
book doesn't show up at all, or it shows up as a **grey rectangle** with no
cover. `kindle-cli` fixes both by converting the book to AZW3, tagging it as a
**personal document (PDOC)**, and pushing it to the device — so the cover
displays and Amazon won't silently delete it.

```console
$ kindle-cli "Flash Boys - Michael Lewis.epub"

▶ Flash Boys - Michael Lewis.epub
  convert → AZW3 (PDOC)
  ✓ pushed (1336020 B) — cover appears after you unplug and the device re-indexes (filed under 'Documents')

summary: 1 ok / 0 failed / 1 total
```

## Why this is needed

Modern Kindles (roughly 2024 onward — Colorsoft, the 2024 Scribe, current
Paperwhite) changed how sideloading works:

1. **Raw EPUB over USB is ignored.** The firmware doesn't index `.epub` files
   copied into `documents/`; the book simply never appears in the library.
   Kindle's native EPUB support only runs through *Send to Kindle*, not USB.
2. **Sideloaded AZW3 gets a grey cover.** A book you convert with Calibre is
   tagged `EBOK` (a store book). The device tries to match it to a Kindle Store
   ASIN, fails (it's your own file), and substitutes a generic grey cover in the
   library grid. It has also been reported that Amazon **auto-deletes** `EBOK`
   sideloads under some sync conditions.
3. **The old fix no longer works.** These devices don't expose the `system/`
   folder over MTP, so you can't push a thumbnail into `system/thumbnails/`, and
   Calibre's cover-sync (which relies on that folder) can't run either.

The one non-jailbreak lever that still works: tag the book as a **personal
document (`PDOC`)**. Personal documents skip the store-ASIN check, so the device
renders the file's own embedded cover — and they aren't subject to the `EBOK`
auto-deletion. The full story is in [`docs/background.md`](docs/background.md).

## How it works

```
EPUB ──①convert (built-in, PDOC-tagged)──▶ AZW3 ──②push──▶ Kindle/documents
```

1. **Convert** EPUB → AZW3 with the built-in pure-Go KF8 writer
   ([leotaku/mobi](https://github.com/leotaku/mobi)) — no Calibre, no external
   tools. KF8 is HTML+CSS underneath, so the book's markup passes through
   nearly verbatim; the cover is embedded and `cdetype` (EXTH 501) is written
   as `PDOC` at generation time.
2. **Push** to the connected Kindle over MTP with `gio`, cleaning up stale
   sidecars so the device re-indexes from scratch.

### Conversion notes

The built-in converter targets reflowable books (novels, non-fiction) and
keeps them intact — text, images, stylesheet, chapter TOC, metadata,
in-book links:

- **Footnote jumps and cross-references work.** In-book hyperlinks are
  converted to KF8 position links (`kindle:pos:fid:…:off:…`), the same form
  Kindle Store books use, so tap-to-jump behaves natively on-device.
- **Images are embedded byte-for-byte.** JPEG/PNG/GIF originals go in
  unchanged — no generation loss, PNG transparency preserved. Only variants
  Kindle firmware can't render (progressive or CMYK JPEG, other formats) are
  re-encoded as baseline JPEG.

One EPUB feature is intentionally simplified:

- **Embedded fonts are dropped.** The device renders with its built-in fonts
  (which cover CJK); this also shrinks the output dramatically.

## Requirements

- **Linux** for the device push (uses `gio` / `gvfs-mtp`). Conversion works
  anywhere; see [Platform notes](#platform-notes).
- A Kindle connected over USB with **Connect** allowed on the device.

That's it — `kindle-cli` is a single static Go binary with **no runtime
dependencies** and cross-compiles for Linux, macOS, and Windows.

## Install

```console
# with Go 1.25+
go install github.com/seapy/kindle-cli@latest

# from source
git clone https://github.com/seapy/kindle-cli
cd kindle-cli
go build -o kindle-cli .
```

## Usage

```console
kindle-cli book.epub                     # convert → PDOC → push (device connected)
kindle-cli book.azw3                     # already AZW3? skip conversion, re-tag + push
kindle-cli ~/books/*.epub                # batch a whole folder
kindle-cli ~/books/                       # a directory works too (*.epub + *.azw3)
kindle-cli "books/*.epub"                # globs expand internally too (Windows cmd)
kindle-cli --no-push book.epub           # just build book.azw3, don't touch the device
kindle-cli --out-dir ./out book.epub     # write the AZW3 to ./out instead
kindle-cli --title "Flash Boys" --author "Michael Lewis" book.epub
```

The converted AZW3 is a kept artifact: it lands **next to the source EPUB**
(same name, `.azw3` extension) so a later run — or a manual copy — can reuse
it without converting again. `--out-dir` redirects it elsewhere. When a
directory is scanned, an `.azw3` sitting next to its same-named `.epub` is
skipped (converting the EPUB reproduces it anyway).

**AZW3/MOBI inputs** (e.g. a Calibre conversion you already have) skip the
conversion step entirely: the file's `cdetype` is checked and — when it isn't
already `PDOC` — re-tagged on a copy before pushing. Your original file is
never modified.

After it finishes, **unplug the Kindle**; the cover appears once the device
re-indexes. The book shows under the **Documents** tab — that's what `PDOC`
means (see the tradeoff below).

### Options

| Option | Effect |
|--------|--------|
| `--no-push` | Convert + patch only; don't connect to a device. |
| `--out-dir DIR` | Write the AZW3 to `DIR` (default: next to the input). |
| `--title` / `--author` | Override metadata (single EPUB input only). |
| `--keep-ebok` | Leave the file tagged `EBOK` (skip the PDOC re-tag). |
| `--no-replace` | Don't overwrite a copy already on the device. |
| `-q`, `--quiet` | Only print errors and the summary. |

## Tradeoffs: PDOC vs EBOK

For a **sideloaded** book (one that isn't a Kindle Store purchase):

- ✅ **Cover shows** in the library grid.
- ✅ **Safe** from Amazon's `EBOK` sideload auto-deletion.
- ➖ Filed under **Documents**, not **Books**.
- ➖ **Vocabulary Builder** doesn't collect words looked up in personal documents.

Store-linked features (X-Ray, "About this book", Goodreads, Popular Highlights)
never worked on sideloaded books anyway, so PDOC costs you nothing there.

## Platform notes

The **push** step is Linux-only (it drives `gio`/`gvfs-mtp`). On macOS or
Windows, run with `--no-push` and copy the resulting `.azw3` (created next to
the EPUB) into the Kindle's `documents/` folder yourself — the PDOC tag is
already applied, so the cover will still show.

## Development

```console
go build -o kindle-cli .
go test ./...
```

The EPUB reader (`internal/epub`), the AZW3 writer (`internal/azw3`), and the
cdetype patcher (`internal/patch`) are all unit-tested without a device.

## Prior art & credits

- **[leotaku/mobi](https://github.com/leotaku/mobi)** — the pure-Go KF8/AZW3
  serializer the built-in converter is built on.
- MobileRead forum threads documenting the 2024 MTP `system/`-folder lockout
  and the resulting cover regression:
  [Kindle Colorsoft — no cover art in library](https://www.mobileread.com/forums/showthread.php?t=364350),
  [sideloaded cover thumbnails don't sync on the 2024 Scribe](https://www.mobileread.com/forums/showthread.php?t=366756).

## License

MIT — see [LICENSE](LICENSE).

> Intended for putting **your own** books on **your own** device. You are
> responsible for complying with the terms of whatever content you load.
