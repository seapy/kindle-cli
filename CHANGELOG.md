# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and this project adheres to
[Semantic Versioning](https://semver.org/).

## [0.0.2] - 2026-07-05

### Added
- `kindle-cli ls` subcommand: lists the connected device's `documents/`
  folder with each book's size and cdetype tag (PDOC/EBOK), so `EBOK`
  strays (grey covers, deletion risk) stand out at a glance. Title/author
  are filled in from the EXTH metadata when the filename doesn't already
  say it; `--all` also shows sidecar (`.sdr`) folders and hidden files.
- One-liner install script (`install.sh`):
  `curl -fsSL https://raw.githubusercontent.com/seapy/kindle-cli/main/install.sh | sh`
  detects the platform, downloads the latest release, verifies its checksum,
  and installs to `~/.local/bin`. Pin or relocate with `KINDLE_CLI_VERSION`
  / `KINDLE_CLI_INSTALL_DIR`.

## [0.0.1] - 2026-07-05

### Added
- Initial release.
- `kindle-cli`: converts EPUBs to AZW3 (KF8) and sideloads them to a
  USB-connected Kindle over MTP (gio/gvfs). Conversion is pure Go
  ([leotaku/mobi](https://github.com/leotaku/mobi) KF8 writer plus a native
  EPUB reader) — no Calibre or other external tools.
- `cdetype` is written as `PDOC` (EXTH 501) at generation time so covers
  show on modern (2024+) Kindles; `--keep-ebok` keeps `EBOK` instead.
- In-book hyperlinks (footnote jumps, cross-references) become real KF8
  position links (`kindle:pos:fid:…:off:…`) — tap-to-jump works on-device.
  Links whose target id is missing fall back to the chapter start.
- Images (JPEG/PNG/GIF) are embedded byte-for-byte — no re-encoding loss,
  PNG transparency preserved. Only variants Kindle firmware can't render
  (progressive/CMYK JPEG, other formats) are re-encoded as baseline JPEG.
- AZW3/MOBI inputs push without conversion: the file's `cdetype` is checked
  and re-tagged to `PDOC` on a copy when needed — the input file is never
  modified. `--keep-ebok` pushes it unchanged.
- Converted AZW3s land next to the source EPUB (same name, `.azw3`
  extension); `--out-dir` redirects them.
- Metadata is filled from CLI overrides, then the OPF, then a
  `Title - Author.epub` filename.
- Directory inputs pick up `*.epub` and `*.azw3` together, skipping an
  `.azw3` whose same-named `.epub` is also being processed. Glob patterns
  are expanded internally, so they work on shells that don't expand them.
- Batch processing with per-file failure isolation.
- `--no-push`, `--out-dir`, `--keep-ebok`, `--no-replace`, `--title`,
  `--author`, `--quiet` options.

### Conversion notes
- Embedded fonts are dropped (the device's built-in fonts are used; output
  is much smaller).
