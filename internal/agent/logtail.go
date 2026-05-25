package agent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/emilejacobs/control-plane/internal/protocol/logtail"
)

// readBlockSize is the chunk we read backwards from EOF when walking
// for the last N lines. 64 KB is a good balance: ~5 reads to gather a
// typical 200-line tail of /var/log/uknomi-agent-error.log, fewer
// syscalls than reading line-by-line, and well within typical Mac
// page-cache windows.
const readBlockSize = 64 * 1024

// binaryDetectionWindow is how many bytes from the tail we inspect for
// non-printable bytes before declaring the file binary. 4 KB matches
// the conventional `file(1)`-style heuristic.
const binaryDetectionWindow = 4 * 1024

// binaryThresholdPercent: more than this percent of bytes in the
// detection window being non-printable (excluding \n, \r, \t) means
// the file is binary and we refuse to return it.
const binaryThresholdPercent = 5

// PerOSAllowList returns the per-OS map of logical log name → file
// path the agent will tail on operator request. Logical names are
// what the dashboard surfaces to the operator; paths are what the
// agent reads. Decoupling the two lets us reorganise file locations
// later without breaking dashboard bookmarks.
//
// See .scratch/phase-2-log-tail/PRD.md § Mac allow-list for the
// canonical list + which logs were explicitly excluded (Zabbix removed
// per fleet_software_deprecations memory, tailscale is oslog-only,
// docker-container logs need a different access pattern).
func PerOSAllowList() map[string]string {
	switch runtime.GOOS {
	case "darwin":
		return map[string]string{
			"agent":        "/var/log/uknomi-agent.log",
			"agent-error":  "/var/log/uknomi-agent-error.log",
			"webui":        "/var/log/uknomi-webui.log",
			"webui-error":  "/var/log/uknomi-webui-error.log",
			"setup":        "/var/log/uknomi-setup.log",
			"install":      "/var/log/install.log",
			"activation":   "/usr/local/etc/uknomi-setup/activation.log",
		}
	case "linux":
		// Linux fleet is deprecating (per fleet_direction memory).
		// Minimal viable set: the agent's own logs only.
		return map[string]string{
			"agent":       "/var/log/uknomi-agent.log",
			"agent-error": "/var/log/uknomi-agent-error.log",
		}
	default:
		return map[string]string{}
	}
}

// TailFile reads the last `lines` lines from path. Caps content at
// maxContent bytes (returns Response.Truncated=true with TruncatedFrom
// set when the cap fires). Refuses files that look binary by the
// detection-window heuristic.
//
// Errors are *logtail.ValidationError with stable error codes
// (CodeBinaryFile, CodeReadError) so the dispatcher can lift them
// into the cmd-result envelope without a translation step.
func TailFile(path string, lines int, maxContent int) (logtail.Response, error) {
	f, err := os.Open(path)
	if err != nil {
		return logtail.Response{}, &logtail.ValidationError{
			Code:    logtail.CodeReadError,
			Message: fmt.Sprintf("open %s: %v", path, err),
		}
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return logtail.Response{}, &logtail.ValidationError{
			Code:    logtail.CodeReadError,
			Message: fmt.Sprintf("stat %s: %v", path, err),
		}
	}
	size := stat.Size()
	if size == 0 {
		return logtail.Response{}, nil
	}

	// Binary detection: peek at the last min(binaryDetectionWindow, size)
	// bytes. We check the TAIL window rather than the head because log
	// files sometimes have binary headers (rare for our targets, but
	// the principle is "what would the operator see?"). If the window
	// is mostly garbage, refuse.
	windowSize := int64(binaryDetectionWindow)
	if size < windowSize {
		windowSize = size
	}
	window := make([]byte, windowSize)
	if _, err := f.ReadAt(window, size-windowSize); err != nil && !errors.Is(err, io.EOF) {
		return logtail.Response{}, &logtail.ValidationError{
			Code:    logtail.CodeReadError,
			Message: fmt.Sprintf("read window from %s: %v", path, err),
		}
	}
	if looksBinary(window) {
		return logtail.Response{}, &logtail.ValidationError{
			Code:    logtail.CodeBinaryFile,
			Message: fmt.Sprintf("%s appears to be binary (>%d%% non-printable bytes in tail window)", path, binaryThresholdPercent),
		}
	}

	// Walk backwards from EOF in readBlockSize chunks, prepending each
	// chunk to a growing buffer until we have `lines` newlines or hit
	// the start of the file.
	var tail []byte
	newlineCount := 0
	offset := size
	for offset > 0 && newlineCount <= lines {
		readSize := int64(readBlockSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		block := make([]byte, readSize)
		if _, err := f.ReadAt(block, offset); err != nil && !errors.Is(err, io.EOF) {
			return logtail.Response{}, &logtail.ValidationError{
				Code:    logtail.CodeReadError,
				Message: fmt.Sprintf("read block from %s: %v", path, err),
			}
		}
		tail = append(block, tail...)
		newlineCount = bytes.Count(tail, []byte{'\n'})
	}

	// Trim to exactly the last `lines` lines if we have more than
	// requested (typical: the last block straddled a line boundary).
	content := keepLastNLines(tail, lines)

	// Content-size cap: if the tail exceeds maxContent, keep the END
	// (most recent) bytes. Re-align to a line boundary so we don't
	// surface a partial first line.
	truncated := false
	truncatedFrom := 0
	if len(content) > maxContent {
		truncated = true
		truncatedFrom = lines
		content = content[len(content)-maxContent:]
		// Drop any leading partial line.
		if i := bytes.IndexByte(content, '\n'); i >= 0 && i+1 < len(content) {
			content = content[i+1:]
		}
	}

	return logtail.Response{
		Content:       string(content),
		Truncated:     truncated,
		TruncatedFrom: truncatedFrom,
	}, nil
}

// keepLastNLines trims data to keep only the last n newline-delimited
// lines (including the trailing newline if present).
func keepLastNLines(data []byte, n int) []byte {
	// Count newlines from the end; stop when we've seen n of them or
	// hit the start of the buffer. The slice starts at the byte AFTER
	// the (n+1)-th newline from the end.
	count := 0
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' {
			count++
			if count == n+1 {
				return data[i+1:]
			}
		}
	}
	return data
}

// looksBinary returns true if the byte slice has more than
// binaryThresholdPercent non-printable bytes (excluding \n \r \t).
// Empty input is not considered binary.
func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	nonPrintable := 0
	for _, b := range data {
		if b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b < 0x20 || b == 0x7f {
			nonPrintable++
		}
	}
	return nonPrintable*100/len(data) > binaryThresholdPercent
}
