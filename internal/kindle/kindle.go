// Package kindle pushes AZW3 files to a USB-connected Kindle over MTP
// (Linux / gvfs / gio).
//
// Uses gio exclusively. Do not mix with libmtp tools (mtp-detect /
// mtp-files) in the same session — they contend for exclusive MTP access and
// the device drops off the bus.
//
// Modern Kindles expose only documents/ (and a few siblings) over MTP; the
// system/ folder is hidden, which is why the old "push a thumbnail into
// system/thumbnails" trick is impossible here and we rely on PDOC instead.
package kindle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var kindleRe = regexp.MustCompile(`mtp://(Amazon_Kindle_[A-Za-z0-9_]+)/`)

type gioResult struct {
	code   int
	stdout string
	stderr string
}

// Kindle is a mounted Kindle's document store, accessed through gvfs.
type Kindle struct {
	Host      string
	Root      string
	Documents string

	gioBin string
}

func gioBin() (string, error) {
	binary, err := exec.LookPath("gio")
	if err != nil {
		return "", errors.New("gio not found — install gvfs / gvfs-mtp (Linux only)")
	}
	return binary, nil
}

func runGio(binary string, args ...string) gioResult {
	cmd := exec.Command(binary, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	return gioResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (k *Kindle) gio(args ...string) gioResult {
	return runGio(k.gioBin, args...)
}

// Detect finds, mounts, and returns the connected Kindle. It errors on
// failure.
func Detect() (*Kindle, error) {
	binary, err := gioBin()
	if err != nil {
		return nil, err
	}
	listing := runGio(binary, "mount", "-li")
	match := kindleRe.FindStringSubmatch(listing.stdout)
	if match == nil {
		return nil, errors.New("no Kindle found — connect via USB and tap 'Connect' on the device")
	}
	host := match[1]
	runGio(binary, "mount", "mtp://"+host+"/") // idempotent if already mounted
	root := fmt.Sprintf("/run/user/%d/gvfs/mtp:host=%s/Internal Storage", os.Getuid(), host)
	if runGio(binary, "info", root+"/documents").code != 0 {
		return nil, errors.New("mount not ready — reconnect the device and retry")
	}
	return &Kindle{
		Host:      host,
		Root:      root,
		Documents: root + "/documents",
		gioBin:    binary,
	}, nil
}

func (k *Kindle) list(path string) []string {
	result := k.gio("list", path)
	if result.code != 0 {
		return nil
	}
	var names []string
	for _, line := range strings.Split(result.stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			names = append(names, line)
		}
	}
	return names
}

// Exists reports whether documents/<name> is present on the device.
func (k *Kindle) Exists(name string) bool {
	return k.gio("info", k.Documents+"/"+name).code == 0
}

// removeSidecars removes the .sdr sidecar folders the device generates for a
// book.
//
// Covers both naming schemes seen in the wild:
//   - filename based:  <stem>.sdr
//   - ASIN based:      <title>_<asin>.sdr (title = stem before " - ")
func (k *Kindle) removeSidecars(stem string, log func(string)) {
	title := stem
	if i := strings.Index(stem, " - "); i >= 0 {
		title = stem[:i]
	}
	asinRe := regexp.MustCompile(regexp.QuoteMeta(title) + `_[0-9A-Fa-f-]+\.sdr$`)
	for _, name := range k.list(k.Documents) {
		if !strings.HasSuffix(name, ".sdr") {
			continue
		}
		if name == stem+".sdr" || asinRe.MatchString(name) {
			sdr := k.Documents + "/" + name
			for _, child := range k.list(sdr) {
				k.gio("remove", sdr+"/"+child)
			}
			if k.gio("remove", sdr).code == 0 {
				log(name)
			}
		}
	}
}

// Push copies azw3 into documents/ and returns the on-device path.
//
// When replace is set and a same-named book already exists, it and its
// sidecars are removed first so the device re-indexes from scratch.
func (k *Kindle) Push(azw3 string, replace bool, log func(string)) (string, error) {
	name := filepath.Base(azw3)
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if replace && k.Exists(name) {
		k.gio("remove", k.Documents+"/"+name)
		k.removeSidecars(stem, func(s string) { log("removed sidecar " + s) })
	}
	result := k.gio("copy", azw3, k.Documents+"/")
	if result.code != 0 {
		msg := strings.TrimSpace(result.stderr)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("gio copy failed: %s", msg)
	}
	return k.Documents + "/" + name, nil
}

// Entry is one item in the device's documents folder.
type Entry struct {
	Name string
	Size int64
	Dir  bool
}

// List returns the contents of documents/, name-sorted.
func (k *Kindle) List() ([]Entry, error) {
	result := k.gio("list", "-l", k.Documents)
	if result.code != 0 {
		msg := strings.TrimSpace(result.stderr)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("gio list failed: %s", msg)
	}
	entries := parseGioList(result.stdout)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// parseGioList parses `gio list -l` output: one "name\tsize\t(type)" per line.
func parseGioList(out string) []Entry {
	var entries []Entry
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || fields[0] == "" {
			continue
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		entries = append(entries, Entry{
			Name: fields[0],
			Size: size,
			Dir:  strings.Contains(fields[2], "directory"),
		})
	}
	return entries
}

// ReadHead returns up to n bytes from the start of documents/<name>, read
// through the gvfs FUSE mount.
func (k *Kindle) ReadHead(name string, n int) ([]byte, error) {
	f, err := os.Open(k.Documents + "/" + name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:read], nil
}

// SizeOnDevice returns the byte size of documents/<name>, or -1 when it
// cannot be determined.
func (k *Kindle) SizeOnDevice(name string) int64 {
	result := k.gio("info", k.Documents+"/"+name)
	for _, line := range strings.Split(result.stdout, "\n") {
		if strings.Contains(line, "standard::size:") {
			field := line[strings.LastIndex(line, ":")+1:]
			if size, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64); err == nil {
				return size
			}
		}
	}
	return -1
}
