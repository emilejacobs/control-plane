package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// System abstracts the OS operations install steps perform, so steps can be
// unit-tested against a fake instead of mutating the host (operator decision:
// all four device modules tested via fakes).
type System interface {
	// Exists reports whether a path exists. A non-IsNotExist stat error is
	// returned (e.g. a permission problem), so steps don't silently treat an
	// unreadable path as absent.
	Exists(path string) (bool, error)
	WriteFile(path string, data []byte, mode os.FileMode) error
	MkdirAll(path string, mode os.FileMode) error
	CopyFile(src, dst string, mode os.FileMode) error
	// Run executes a command (e.g. launchctl) to completion.
	Run(ctx context.Context, name string, args ...string) error
}

// osSystem is the real System backed by os/exec. macOS-only in practice
// (launchctl); the ADR-034 backend split keeps OS-specific verbs at this edge.
type osSystem struct{}

// NewOSSystem returns the production System.
func NewOSSystem() System { return osSystem{} }

func (osSystem) Exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (osSystem) WriteFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}

func (osSystem) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}

func (osSystem) CopyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func (osSystem) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}
