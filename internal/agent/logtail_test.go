package agent_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent"
	"github.com/emilejacobs/control-plane/internal/protocol/logtail"
)

// Happy path: a small text file with N lines, request N → all lines
// returned verbatim, no truncation, no error.
func TestTailFileSmallExactCount(t *testing.T) {
	path := writeLines(t, "small.log", []string{"line 1", "line 2", "line 3"})

	resp, err := agent.TailFile(path, 3, logtail.MaxContentSize)
	if err != nil {
		t.Fatalf("TailFile: %v", err)
	}
	if resp.Truncated {
		t.Error("Truncated: got true, want false")
	}
	if resp.Content != "line 1\nline 2\nline 3\n" {
		t.Errorf("content: got %q", resp.Content)
	}
}

// Request fewer lines than file has → returns the LAST N (tail
// semantics, not head). This is the keystone test.
func TestTailFileReturnsLastN(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "L" + itoa(i+1)
	}
	path := writeLines(t, "many.log", lines)

	resp, err := agent.TailFile(path, 5, logtail.MaxContentSize)
	if err != nil {
		t.Fatalf("TailFile: %v", err)
	}
	if resp.Truncated {
		t.Error("should not be truncated at content cap")
	}
	want := "L96\nL97\nL98\nL99\nL100\n"
	if resp.Content != want {
		t.Errorf("content:\ngot:  %q\nwant: %q", resp.Content, want)
	}
}

// Request more lines than file has → returns all lines, no truncation.
func TestTailFileShortFile(t *testing.T) {
	path := writeLines(t, "short.log", []string{"a", "b"})

	resp, err := agent.TailFile(path, 100, logtail.MaxContentSize)
	if err != nil {
		t.Fatalf("TailFile: %v", err)
	}
	if resp.Truncated {
		t.Error("should not be truncated")
	}
	if resp.Content != "a\nb\n" {
		t.Errorf("content: got %q", resp.Content)
	}
}

// Empty file → empty content, no error, not truncated.
func TestTailFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := agent.TailFile(path, 10, logtail.MaxContentSize)
	if err != nil {
		t.Fatalf("TailFile: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("content: got %q, want empty", resp.Content)
	}
	if resp.Truncated {
		t.Error("empty file shouldn't be truncated")
	}
}

// Content-size cap fires: 500 lines of 1KB each, request all 500
// with a 200KB cap → truncated=true, content ≤ 200KB, truncatedFrom
// = lines requested. Content keeps the most recent bytes (we tail
// backwards).
func TestTailFileTruncatesByContentSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")
	// 1000 lines × ~1100 bytes each = ~1.1 MB total
	var buf bytes.Buffer
	for i := 0; i < 1000; i++ {
		buf.WriteString("L" + itoa(i+1) + " ")
		buf.WriteString(strings.Repeat("x", 1100))
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	const cap = 200 * 1024
	resp, err := agent.TailFile(path, 1000, cap)
	if err != nil {
		t.Fatalf("TailFile: %v", err)
	}
	if !resp.Truncated {
		t.Error("Truncated: got false, want true (content cap should fire)")
	}
	if resp.TruncatedFrom != 1000 {
		t.Errorf("TruncatedFrom: got %d, want 1000", resp.TruncatedFrom)
	}
	if len(resp.Content) > cap {
		t.Errorf("content size: got %d, want ≤ %d", len(resp.Content), cap)
	}
	// Most-recent content should be in there: L1000 line should appear.
	if !strings.Contains(resp.Content, "L1000") {
		t.Error("expected the last line (L1000) in the truncated tail")
	}
	// The first lines (L1, L2…) should NOT be there.
	if strings.Contains(resp.Content, "L1 ") {
		t.Error("first line (L1) should not be in the tail")
	}
}

// Binary file detection: a file with >5% non-printable bytes is
// refused with CodeBinaryFile so the dashboard renders a clear error
// instead of garbled output.
func TestTailFileRejectsBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.dat")
	// 4KB of mostly-null bytes — clearly binary.
	data := make([]byte, 4096)
	for i := range data {
		if i%10 == 0 {
			data[i] = byte('A')
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := agent.TailFile(path, 100, logtail.MaxContentSize)
	if err == nil {
		t.Fatal("expected error on binary file")
	}
	v, ok := logtail.AsValidation(err)
	if !ok || v.Code != logtail.CodeBinaryFile {
		t.Errorf("error: got %v, want code %q", err, logtail.CodeBinaryFile)
	}
}

// Non-existent file: clear error, doesn't crash.
func TestTailFileNonexistent(t *testing.T) {
	_, err := agent.TailFile("/does/not/exist.log", 100, logtail.MaxContentSize)
	if err == nil {
		t.Fatal("expected error on missing file")
	}
	v, ok := logtail.AsValidation(err)
	if !ok || v.Code != logtail.CodeReadError {
		t.Errorf("error: got %v, want code %q", err, logtail.CodeReadError)
	}
}

// PerOSAllowList returns a non-empty map on darwin (Mac fleet) and is
// exposed so the dispatcher handler can resolve log_name → Entry
// without re-implementing the allow-list. Entries carry the Kind
// discriminator the agent's resolver switches on (issue #7 / ADR-030
// § 5).
func TestPerOSAllowListShape(t *testing.T) {
	list := agent.PerOSAllowList()
	if list == nil {
		t.Fatal("PerOSAllowList returned nil")
	}
	// On darwin we expect at least "agent" + "agent-error" + "webui".
	// On other OSes we still expect SOMETHING (even if just "agent").
	entry, ok := list["agent"]
	if !ok {
		t.Fatal(`PerOSAllowList missing "agent" — should be on every OS`)
	}
	if entry.Kind != logtail.KindFile {
		t.Errorf(`"agent" entry kind: got %q, want %q`, entry.Kind, logtail.KindFile)
	}
	// Sanity: file targets are absolute paths.
	for name, e := range list {
		if e.Kind == logtail.KindFile && !filepath.IsAbs(e.Target) {
			t.Errorf("%s: file target %q is not absolute", name, e.Target)
		}
		if e.Name != name {
			t.Errorf("%s: entry.Name=%q does not match map key", name, e.Name)
		}
		if e.Kind != logtail.KindFile && e.Kind != logtail.KindDocker {
			t.Errorf("%s: unknown kind %q", name, e.Kind)
		}
		if e.Label == "" {
			t.Errorf("%s: empty label", name)
		}
	}
}

// On darwin, the allow-list adds a docker entry for the
// plate-recognizer container (issue #7). The seven file entries from
// slice 3 stay; the new entry uses KindDocker + container name as
// Target. This pins the default Mac allow-list against drift.
func TestPerOSAllowListDarwinHasPlateRecognizerDocker(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only allow-list")
	}
	list := agent.PerOSAllowList()
	pr, ok := list["plate-recognizer"]
	if !ok {
		t.Fatal("plate-recognizer entry missing on darwin allow-list")
	}
	if pr.Kind != logtail.KindDocker {
		t.Errorf("kind: got %q, want %q", pr.Kind, logtail.KindDocker)
	}
	if pr.Target != "plate-recognizer-stream" {
		t.Errorf("target: got %q, want %q", pr.Target, "plate-recognizer-stream")
	}
	if pr.Label != "Plate Recognizer (Docker)" {
		t.Errorf("label: got %q, want %q", pr.Label, "Plate Recognizer (Docker)")
	}
	// Still the seven file entries.
	for _, name := range []string{"agent", "agent-error", "webui", "webui-error", "setup", "install", "activation"} {
		e, ok := list[name]
		if !ok {
			t.Errorf("%s: missing from darwin allow-list", name)
			continue
		}
		if e.Kind != logtail.KindFile {
			t.Errorf("%s: kind got %q, want %q", name, e.Kind, logtail.KindFile)
		}
	}
}

// --- helpers ---

func writeLines(t *testing.T, name string, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ensure errors.Is import isn't a noop in the lints; used below
var _ = errors.Is
