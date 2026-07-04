// Unit tests for gio output parsing — no device required.
package kindle

import "testing"

func TestParseGioList(t *testing.T) {
	out := "우버 인사이드 - 애덤 라신스키.azw3\t2271302\t(regular)\n" +
		"dictionaries\t0\t(directory)\n" +
		".cache\t0\t(directory)\n" +
		"소프트웨어 엔지니어 가이드북.epub\t13574223\t(regular)\n" +
		"\n" + // trailing blank line
		"garbage line without tabs\n"
	entries := parseGioList(out)
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4: %+v", len(entries), entries)
	}
	first := entries[0]
	if first.Name != "우버 인사이드 - 애덤 라신스키.azw3" || first.Size != 2271302 || first.Dir {
		t.Errorf("first entry = %+v", first)
	}
	if !entries[1].Dir || entries[1].Name != "dictionaries" {
		t.Errorf("directory entry = %+v", entries[1])
	}
}
