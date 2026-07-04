// Command kindle-cli converts EPUBs to Kindle personal documents
// (PDOC AZW3) and sideloads them so covers show on modern (2024+) Kindles.
// Already-converted AZW3/MOBI files are pushed without conversion.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/seapy/kindle-cli/internal/azw3"
	"github.com/seapy/kindle-cli/internal/epub"
	"github.com/seapy/kindle-cli/internal/kindle"
	"github.com/seapy/kindle-cli/internal/patch"
)

// version is stamped by goreleaser (-X main.version=…) on release builds;
// the git tag is the single source of truth, nothing to bump in source.
var version = ""

func versionString() string {
	if version != "" {
		return version
	}
	// `go install …@vX.Y.Z` builds carry the module version instead
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}

type options struct {
	noPush    bool
	outDir    string
	title     string
	author    string
	keepEBOK  bool
	noReplace bool
	quiet     bool
}

func (o *options) log(msg string) {
	if !o.quiet {
		fmt.Println(msg)
	}
}

// expandUser replaces a leading "~" with the user's home directory.
func expandUser(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// collectInputs expands file/dir/glob inputs into a sorted list of book
// paths. Globs are expanded here too, so patterns work even on shells that
// don't expand them (e.g. Windows cmd).
//
// A directory scan skips an .azw3 when the same-named .epub is also being
// picked up: converting the EPUB (re)produces exactly that .azw3 next to it,
// so processing both would push the same book twice.
func collectInputs(inputs []string) []string {
	var collected []string
	for _, item := range inputs {
		path := expandUser(item)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			epubs, _ := filepath.Glob(filepath.Join(path, "*.epub"))
			azw3s, _ := filepath.Glob(filepath.Join(path, "*.azw3"))
			matches := epubs
			for _, a := range azw3s {
				if _, err := os.Stat(strings.TrimSuffix(a, ".azw3") + ".epub"); err != nil {
					matches = append(matches, a)
				}
			}
			sort.Strings(matches)
			collected = append(collected, matches...)
			continue
		} else if err == nil {
			collected = append(collected, path)
			continue
		}
		if strings.ContainsAny(path, "*?[") {
			if matches, _ := filepath.Glob(path); len(matches) > 0 {
				sort.Strings(matches)
				collected = append(collected, matches...)
				continue
			}
		}
		fmt.Fprintf(os.Stderr, "! input not found: %s\n", item)
	}
	return collected
}

func process(path string, opts *options, device *kindle.Kindle) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		return processEPUB(path, opts, device)
	case ".azw3", ".mobi":
		return processAZW3(path, opts, device)
	default:
		return fmt.Errorf("unsupported input type (expected .epub, .azw3, or .mobi)")
	}
}

// processAZW3 pushes an already-converted AZW3/MOBI, re-tagging a copy as
// PDOC first when needed. The input file is never modified.
func processAZW3(path string, opts *options, device *kindle.Kindle) error {
	if opts.title != "" || opts.author != "" {
		return fmt.Errorf("--title/--author only apply to EPUB inputs")
	}
	opts.log(fmt.Sprintf("\n▶ %s", filepath.Base(path)))

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, current, cdeErr := patch.FindCDEType(data)

	needPatch := false
	switch {
	case opts.keepEBOK:
		opts.log("  cdetype: left as-is (--keep-ebok)")
	case cdeErr != nil:
		return fmt.Errorf("cannot re-tag as PDOC (%v); push unchanged with --keep-ebok", cdeErr)
	case string(current) == "PDOC":
		opts.log("  cdetype: already PDOC")
	default:
		needPatch = true
	}

	pushPath := path
	if needPatch || opts.outDir != "" {
		if needPatch {
			data, err = patch.PatchCDETypeBytes(data, patch.PDOC)
			if err != nil {
				return err
			}
			opts.log(fmt.Sprintf("  cdetype: %s → PDOC (on a copy; original untouched)", current))
		}
		var tmp string
		dir := opts.outDir
		if dir != "" {
			dir = expandUser(dir)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		} else {
			if tmp, err = os.MkdirTemp("", "kindle-cli-"); err != nil {
				return err
			}
			dir = tmp
		}
		defer func() {
			if tmp != "" {
				os.RemoveAll(tmp)
			}
		}()
		pushPath = filepath.Join(dir, filepath.Base(path))
		if samePath(pushPath, path) {
			return fmt.Errorf("--out-dir points at the input file itself; choose another directory")
		}
		if err := os.WriteFile(pushPath, data, 0o644); err != nil {
			return err
		}
	}

	if opts.noPush {
		if pushPath == path {
			opts.log("  ✓ nothing to build (already PDOC; use --out-dir for a copy)")
		} else {
			opts.log(fmt.Sprintf("  ✓ built %s", pushPath))
		}
		return nil
	}
	return pushAndVerify(device, pushPath, opts)
}

func processEPUB(epubPath string, opts *options, device *kindle.Kindle) error {
	opts.log(fmt.Sprintf("\n▶ %s", filepath.Base(epubPath)))
	stem := strings.TrimSuffix(filepath.Base(epubPath), filepath.Ext(epubPath))

	// the converted AZW3 is a kept artifact: next to the source EPUB by
	// default, or in --out-dir
	outAZW3 := filepath.Join(filepath.Dir(epubPath), stem+".azw3")
	if opts.outDir != "" {
		outDir := expandUser(opts.outDir)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		outAZW3 = filepath.Join(outDir, stem+".azw3")
	}

	book, err := epub.Read(epubPath)
	if err != nil {
		return err
	}
	meta := epub.ResolveMetadata(book, epubPath, opts.title, opts.author)
	note := ""
	if meta.TitleBackfilled || meta.AuthorBackfilled {
		note = fmt.Sprintf("  (backfilled metadata: %s / %s)", meta.Title, meta.Author)
	}
	docType := "PDOC"
	if opts.keepEBOK {
		docType = "EBOK"
	}
	opts.log(fmt.Sprintf("  convert → AZW3 (%s)%s", docType, note))
	warnings, err := azw3.Write(book, outAZW3, azw3.Options{
		Title:   meta.Title,
		Author:  meta.Author,
		DocType: docType,
	})
	for _, w := range warnings {
		opts.log("  ! " + w)
	}
	if err != nil {
		return err
	}

	if opts.noPush {
		opts.log(fmt.Sprintf("  ✓ built %s", outAZW3))
		return nil
	}
	return pushAndVerify(device, outAZW3, opts)
}

// samePath reports whether two paths refer to the same file location.
func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

// pushAndVerify copies the book to the device and confirms the on-device
// size matches.
func pushAndVerify(device *kindle.Kindle, path string, opts *options) error {
	target, err := device.Push(path, !opts.noReplace, func(m string) { opts.log("  ↳ " + m) })
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	localSize := info.Size()
	deviceSize := device.SizeOnDevice(filepath.Base(target))
	if deviceSize != localSize {
		opts.log(fmt.Sprintf("  ! size mismatch: device %d vs local %d", deviceSize, localSize))
	} else {
		opts.log(fmt.Sprintf(
			"  ✓ pushed (%d B) — cover appears after you unplug and the device re-indexes (filed under 'Documents')",
			localSize))
	}
	return nil
}

func run() int {
	opts := &options{}
	showVersion := false

	fs := flag.NewFlagSet("kindle-cli", flag.ExitOnError)
	fs.BoolVar(&opts.noPush, "no-push", false, "convert + patch only; don't touch the device")
	fs.StringVar(&opts.outDir, "out-dir", "", "directory for the AZW3 output (default: next to the input)")
	fs.StringVar(&opts.title, "title", "", "override title (single input only)")
	fs.StringVar(&opts.author, "author", "", "override author (single input only)")
	fs.BoolVar(&opts.keepEBOK, "keep-ebok", false, "do not re-tag as PDOC (leave EBOK)")
	fs.BoolVar(&opts.noReplace, "no-replace", false, "do not overwrite a copy already on the device")
	fs.BoolVar(&opts.quiet, "q", false, "only print errors/summary")
	fs.BoolVar(&opts.quiet, "quiet", false, "only print errors/summary")
	fs.BoolVar(&showVersion, "version", false, "show version and exit")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `usage: kindle-cli [options] inputs...

Convert EPUBs to Kindle personal documents (PDOC AZW3) and sideload them so
covers show on modern (2024+) Kindles. AZW3/MOBI inputs skip conversion and
are pushed as-is (re-tagged PDOC on a copy when needed).

positional arguments:
  inputs      EPUB/AZW3/MOBI file(s), a directory, or a glob

options:
`)
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[1:])

	if showVersion {
		fmt.Printf("kindle-cli %s\n", versionString())
		return 0
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}

	epubs := collectInputs(fs.Args())
	if len(epubs) == 0 {
		fmt.Fprintln(os.Stderr, "✗ no EPUBs to process")
		return 2
	}
	if (opts.title != "" || opts.author != "") && len(epubs) > 1 {
		fmt.Fprintln(os.Stderr, "✗ --title/--author only allowed with a single input")
		return 2
	}

	var device *kindle.Kindle
	if !opts.noPush {
		var err error
		device, err = kindle.Detect()
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			return 3
		}
	}

	ok, failed := 0, 0
	for _, epub := range epubs {
		if err := process(epub, opts, device); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", filepath.Base(epub), err)
			failed++
		} else {
			ok++
		}
	}

	fmt.Printf("\nsummary: %d ok / %d failed / %d total\n", ok, failed, len(epubs))
	if failed > 0 {
		return 1
	}
	return 0
}

func main() {
	os.Exit(run())
}
