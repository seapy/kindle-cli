# Background: why sideloaded covers break on modern Kindles, and how PDOC fixes it

This document explains the problem `kindle-cli` solves and why the approach it
takes is the right one for 2024-era Kindles. It's written for people who want to
understand or verify the mechanism, not just run the tool.

## TL;DR

- Modern Kindles **don't index raw EPUB** copied over USB → you must convert to
  a native format (AZW3).
- A Calibre AZW3 is tagged **`EBOK`** (a store book). Because it has no matching
  **Kindle Store ASIN**, the device shows a **grey placeholder cover** in the
  library, and Amazon may **auto-delete** it.
- 2024 devices **hide the `system/` folder over MTP**, so the historical fix
  (writing a thumbnail into `system/thumbnails/`, which Calibre automates) is
  **impossible** — for us *and* for Calibre.
- Re-tagging the file as a **personal document (`PDOC`)** makes the device use
  the file's **embedded cover** and exempts it from `EBOK` auto-deletion. This
  is the only reliable non-jailbreak fix on these devices.

## The device landscape shift: USB Mass Storage → MTP

Older Kindles mounted as a **USB Mass Storage** device: a real filesystem you
could read and write freely, including the hidden `system/` directory where the
library's cover thumbnails live (`system/thumbnails/thumbnail_<id>_EBOK_portrait.jpg`).
Every "fix your Kindle covers" guide — and Calibre's own cover-sync — depends on
writing into that folder.

2024 devices (Kindle Colorsoft, the 2024 Scribe, current Paperwhite) connect as
**MTP** instead, and Amazon curates what MTP exposes. Enumerating the raw MTP
object tree on a Colorsoft shows only:

```
Internal Storage/
  documents/          ← books, dictionaries, .sdr sidecars, .cache/
  screenshots/
  fonts/
  audible/
```

There is **no `system/` folder anywhere in the MTP object tree** — not hidden,
not permission-denied, simply not published by the device. Any host tool that
speaks MTP (including Calibre, which uses libmtp on Linux) sees the same
restricted view. So the thumbnail-push fix cannot run on these devices at all.

## Why raw EPUB doesn't show up

Kindle firmware indexes the formats it natively renders for USB sideload:
**AZW3/KF8, KFX, PDF, TXT**. It does **not** index `.epub` dropped into
`documents/` over USB — no sidecar is created, and the book never appears in the
library. (Amazon's "Kindle now supports EPUB" refers to *Send to Kindle*, which
converts EPUB to KFX server-side; it is not USB sideload.)

You can confirm this on a device: an AZW3 you push gets a `<name>.sdr` sidecar
folder generated next to it after indexing; a raw `.epub` gets none.

## The grey cover: `cdetype`, EBOK vs PDOC, and the ASIN cross-check

Every MOBI/AZW3 file carries an **EXTH header** (a table of typed metadata
records) inside PalmDB record 0. One record — **type 501, `cdetype`** — declares
what kind of content this is:

- **`EBOK`** — an ebook / Kindle Store book. Calibre writes this by default.
- **`PDOC`** — a personal document.

When the device indexes an `EBOK` file, it tries to **cross-reference it against
the Kindle Store** using the file's ASIN. A sideloaded book either has no store
ASIN or (as with a Calibre conversion) a **random UUID** in the ASIN field that
matches nothing in the store. The cross-check fails, and the device substitutes
a **generic grey cover** in the library grid.

Crucially, this only affects the **library thumbnail**, not the file:

- The **lock screen / open book** renders the file's **embedded cover directly**
  → it looks fine.
- The **library grid** uses a **separate thumbnail cache** in `system/` →
  greyed out.

That split is the tell-tale signature of this exact problem, and it's why you
often see "the cover shows on the lock screen but not in the library."

Personal documents (`PDOC`) are **not** subjected to the store cross-check. The
device just uses the embedded cover — so flipping `cdetype` from `EBOK` to
`PDOC` makes the library thumbnail appear, using nothing but the cover already
inside the file.

## The `EBOK` auto-deletion angle

Beyond covers, Amazon has been observed **automatically removing** sideloaded
`EBOK`-tagged books from devices under certain sync conditions (e.g. after a
period in airplane mode followed by reconnecting). Personal documents are user
content and are **not** deleted this way. So the `EBOK → PDOC` re-tag protects
your sideloads from disappearing, independent of the cover benefit. (This is the
original motivation behind tools like `ebook_cdetype_to_pdoc`.)

## Why an in-place byte patch is enough

`EBOK` and `PDOC` are both exactly **four ASCII bytes**. The value sits in the
EXTH record payload; replacing it doesn't change the record length, the EXTH
table length, or any PalmDB record offsets. So `kindle-cli` locates EXTH record
501 and overwrites those 4 bytes in place — no re-encoding, no dependency beyond
the standard library, and no risk of corrupting the container.

The parser walks the container minimally:

1. PalmDB record-0 offset is a big-endian `uint32` at file offset **78**.
2. Find `EXTH` at/after record 0; the record **count** is a `uint32` at
   `exth + 8`.
3. Iterate `count` records of `(uint32 type, uint32 len, payload[len-8])` and
   stop at type **501**.

See `internal/patch/patch.go` and `internal/patch/patch_test.go`.

## What you give up with PDOC

Re-tagging as a personal document is not free:

- The book is filed under the **Documents** tab, not **Books**.
- **Vocabulary Builder** does not collect words looked up in personal documents
  (it's a books-only feature).

Store-linked features (X-Ray, "About this book", Popular Highlights, Goodreads)
require a store-matched book and never worked on sideloaded `EBOK` files either,
so PDOC costs nothing there.

## Alternatives considered

- **Push a thumbnail into `system/thumbnails/`** (the classic fix / Calibre's
  cover-sync): **impossible** on 2024 devices — `system/` isn't exposed over MTP.
- **Set a real Kindle Store ASIN** so the device pulls the store cover: only
  works for books that *exist* in the Kindle Store, and risks showing the wrong
  cover. Useless for books that aren't sold on Amazon.
- **Send to Kindle**: Amazon processes the book server-side and delivers it with
  a proper cover — but it means **uploading your (DRM-stripped) book to Amazon**,
  which many people specifically want to avoid.
- **Jailbreak** (WinterBreak / AdBreak, depending on firmware): gives full Linux
  access and lets you write `system/thumbnails/` directly — maximum control, but
  firmware-gated and higher risk.

`kindle-cli` picks the **PDOC** route: no uploads, no jailbreak, works over
plain USB/MTP, and additionally hardens the book against auto-deletion.

## References

- MobileRead — *Covers not showing when sideloaded via USB*, and the 2024 Scribe
  cover-sync thread documenting the MTP `system/`-folder lockout.
- Reporting on Amazon auto-deleting sideloaded ebooks from Kindles (2025).
- `krwow/ebook_cdetype_to_pdoc` — prior art on the `EBOK → PDOC` tag change.
- MOBI/AZW3 / EXTH header format (Calibre source, MobileRead wiki).
