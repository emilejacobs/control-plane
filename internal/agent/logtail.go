package agent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	logtail "github.com/emilejacobs/control-plane/internal/protocol/logtail"
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

// PerOSAllowList returns the per-OS map of logical log name → Entry
// the agent will fetch on operator request. Logical names are what
// the dashboard surfaces to the operator; the Entry's Kind picks the
// executor (file tail vs docker logs) and Target is the kind-specific
// identifier. Decoupling the logical name from the underlying source
// lets us reorganise file locations or swap container names later
// without breaking dashboard bookmarks.
//
// See .scratch/phase-2-log-tail/PRD.md § Mac allow-list for the
// canonical file list + ADR-030 § 5 for the docker extension (issue
// #7). Zabbix dropped per fleet_software_deprecations memory; tailscale
// stays oslog-only.
func PerOSAllowList() map[string]logtail.Entry {
	switch runtime.GOOS {
	case "darwin":
		return map[string]logtail.Entry{
			"agent":            {Name: "agent", Kind: logtail.KindFile, Target: "/var/log/uknomi-agent.log", Label: "uknomi-agent (stdout)"},
			"agent-error":      {Name: "agent-error", Kind: logtail.KindFile, Target: "/var/log/uknomi-agent-error.log", Label: "uknomi-agent (stderr / slog)"},
			"webui":            {Name: "webui", Kind: logtail.KindFile, Target: "/var/log/uknomi-webui.log", Label: "Edge UI (stdout)"},
			"webui-error":      {Name: "webui-error", Kind: logtail.KindFile, Target: "/var/log/uknomi-webui-error.log", Label: "Edge UI (stderr)"},
			"setup":            {Name: "setup", Kind: logtail.KindFile, Target: "/var/log/uknomi-setup.log", Label: "Setup script"},
			"install":          {Name: "install", Kind: logtail.KindFile, Target: "/var/log/install.log", Label: "macOS installer"},
			"activation":       {Name: "activation", Kind: logtail.KindFile, Target: "/usr/local/etc/uknomi-setup/activation.log", Label: "Edge UI activation"},
			"plate-recognizer": {Name: "plate-recognizer", Kind: logtail.KindDocker, Target: "plate-recognizer-stream", Label: "Plate Recognizer (Docker)"},
		}
	case "linux":
		// Linux fleet is deprecating (per fleet_direction memory).
		// Minimal viable set: the agent's own logs only. Docker
		// entries are deliberately Mac-only here — Linux installs
		// don't ship Plate Recognizer today.
		return map[string]logtail.Entry{
			"agent":       {Name: "agent", Kind: logtail.KindFile, Target: "/var/log/uknomi-agent.log", Label: "uknomi-agent (stdout)"},
			"agent-error": {Name: "agent-error", Kind: logtail.KindFile, Target: "/var/log/uknomi-agent-error.log", Label: "uknomi-agent (stderr / slog)"},
		}
	default:
		return map[string]logtail.Entry{}
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

// TailDocker fetches the last `lines` lines of the named container's
// stdout+stderr via `docker logs --tail N <container>`. Caps content
// at maxContent and reports truncation. Issue #7 / ADR-030 § 5.
//
// The actual docker invocation is behind the dockerLogsFn function
// pointer so handler unit tests can inject a fake without shelling
// out. Production wires execDockerLogs (defined in logtail_docker.go).
func TailDocker(container string, lines int, maxContent int) (logtail.Response, error) {
	out, err := dockerLogsFn(container, lines)
	if err != nil {
		return logtail.Response{}, &logtail.ValidationError{
			Code:    logtail.CodeReadError,
			Message: fmt.Sprintf("docker logs %s: %v", container, err),
		}
	}
	truncated := false
	truncatedFrom := 0
	if len(out) > maxContent {
		truncated = true
		truncatedFrom = lines
		out = out[len(out)-maxContent:]
		if i := bytes.IndexByte(out, '\n'); i >= 0 && i+1 < len(out) {
			out = out[i+1:]
		}
	}
	return logtail.Response{
		Content:       string(out),
		Truncated:     truncated,
		TruncatedFrom: truncatedFrom,
	}, nil
}

// dockerLogsFn is the seam tests swap. Production points it at
// execDockerLogs (defined in logtail_docker.go); tests assign a fake
// closure in setup.
var dockerLogsFn = func(container string, lines int) ([]byte, error) {
	return nil, errors.New("docker executor not wired — set dockerLogsFn")
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
