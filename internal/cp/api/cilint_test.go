package api_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandlersDoNotImportSlogDirectly enforces ADR-011 structurally: handler
// files under internal/cp/api/handlers/ must not import "log/slog" directly.
// Logging belongs to cplog.FromContext(r.Context()) so every line carries
// the request's correlation_id without per-call boilerplate.
//
// A handler that bypasses this contract — e.g. import "log/slog"; slog.Info(...) —
// will produce log lines untagged by correlation_id and silently break
// cross-service debugging. This gate fails fast in CI.
func TestHandlersDoNotImportSlogDirectly(t *testing.T) {
	violations, err := scanForBadImport(filepath.Join("handlers"), "log/slog")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("handler files must use cplog.FromContext, not log/slog directly. Violators:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestSlogImportScannerCatchesViolation proves the scanner above actually
// flags a bad file — without this we can't tell whether a green run means
// "all clean" or "scanner silently broken."
func TestSlogImportScannerCatchesViolation(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad_handler.go")
	src := `package bad

import "log/slog"

func H() { slog.Info("oops") }
`
	if err := os.WriteFile(bad, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	violations, err := scanForBadImport(dir, "log/slog")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if !strings.HasSuffix(violations[0], "bad_handler.go") {
		t.Errorf("violation should name bad_handler.go; got %q", violations[0])
	}
}

func scanForBadImport(root, importPath string) ([]string, error) {
	want := `"` + importPath + `"`
	var hits []string
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == want {
				hits = append(hits, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}
