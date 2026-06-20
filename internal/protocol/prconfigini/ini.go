// Package prconfigini is a minimal configobj-style parser for Plate Recognizer
// Stream's config.ini and the merge of CP-managed fields onto it (issue #5,
// ADR-030 §3, ADR-038).
//
// PR Stream uses configobj's nested-section format: depth is the bracket count
// ([cameras] = depth 1, [[66_3]] = depth 2, …), keys are `key = value`. The
// agent MERGES the CP-managed fields (region, the LPR camera url, and the
// webhook targets) onto the existing on-disk file and preserves everything else
// — the many hand-tuned [cameras] fields and per-webhook fields like
// `header = MAC:…`, `image_quality`, `image_type` — rather than rendering a
// fresh file. CP is source of truth only for the subset it manages.
package prconfigini

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"

	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

// item is one element of a section's ordered body: a key/value, a nested
// section, or a preserved raw line (blank/comment).
type item struct {
	kv      *kv
	section *Section
	raw     string // verbatim blank/comment line (no kv/section set)
}

type kv struct {
	key   string
	value string
}

// Section is a configobj section: ordered items, nested by depth (root = 0).
type Section struct {
	name  string
	depth int
	items []item
}

// Doc is a parsed config.ini (a virtual depth-0 root section).
type Doc struct{ root *Section }

var sectionLine = func(trimmed string) (name string, depth int, ok bool) {
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return "", 0, false
	}
	d := 0
	for d < len(trimmed) && trimmed[d] == '[' {
		d++
	}
	// Must close with exactly d ']' and have a non-empty name between.
	if d == 0 || len(trimmed) < 2*d+1 || strings.Count(trimmed, "[") != d || strings.Count(trimmed, "]") != d {
		return "", 0, false
	}
	return strings.TrimSpace(trimmed[d : len(trimmed)-d]), d, true
}

// Parse reads config.ini bytes into a Doc, preserving order, nesting, and
// blank/comment lines.
func Parse(data []byte) (*Doc, error) {
	root := &Section{depth: 0}
	stack := []*Section{root}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		cur := stack[len(stack)-1]

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			cur.items = append(cur.items, item{raw: line})
			continue
		}
		if name, depth, ok := sectionLine(trimmed); ok {
			if depth > len(stack) { // jumped a level — treat parent as the deepest available
				depth = len(stack)
			}
			stack = stack[:depth] // keep 0..depth-1
			parent := stack[depth-1]
			sec := &Section{name: name, depth: depth}
			parent.items = append(parent.items, item{section: sec})
			stack = append(stack, sec)
			continue
		}
		if eq := strings.IndexByte(line, '='); eq >= 0 {
			cur.items = append(cur.items, item{kv: &kv{
				key:   strings.TrimSpace(line[:eq]),
				value: strings.TrimSpace(line[eq+1:]),
			}})
			continue
		}
		// Unknown line shape — preserve verbatim.
		cur.items = append(cur.items, item{raw: line})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan config.ini: %w", err)
	}
	return &Doc{root: root}, nil
}

// sub returns the direct child section named n, or nil.
func (s *Section) sub(n string) *Section {
	for _, it := range s.items {
		if it.section != nil && it.section.name == n {
			return it.section
		}
	}
	return nil
}

// ensureSub returns the child section named n, creating it (appended) if absent.
func (s *Section) ensureSub(n string) *Section {
	if got := s.sub(n); got != nil {
		return got
	}
	sec := &Section{name: n, depth: s.depth + 1}
	s.items = append(s.items, item{section: sec})
	return sec
}

// setKV updates the value of key in place, or appends it before the first nested
// subsection (configobj requires a section's scalars to precede its subsections).
func (s *Section) setKV(key, value string) {
	for i := range s.items {
		if s.items[i].kv != nil && s.items[i].kv.key == key {
			s.items[i].kv.value = value
			return
		}
	}
	insertAt := len(s.items)
	for i, it := range s.items {
		if it.section != nil {
			insertAt = i
			break
		}
	}
	newItem := item{kv: &kv{key: key, value: value}}
	s.items = append(s.items[:insertAt], append([]item{newItem}, s.items[insertAt:]...)...)
}

// Bytes serializes the Doc back to config.ini text (4-space indent per depth).
func (d *Doc) Bytes() []byte {
	var b bytes.Buffer
	writeSection(&b, d.root)
	return b.Bytes()
}

func writeSection(b *bytes.Buffer, s *Section) {
	for _, it := range s.items {
		switch {
		case it.raw != "" || (it.kv == nil && it.section == nil):
			b.WriteString(it.raw)
			b.WriteByte('\n')
		case it.kv != nil:
			b.WriteString(strings.Repeat("    ", s.depth))
			b.WriteString(it.kv.key)
			b.WriteString(" = ")
			b.WriteString(it.kv.value)
			b.WriteByte('\n')
		case it.section != nil:
			sec := it.section
			b.WriteString(strings.Repeat("    ", sec.depth-1))
			b.WriteString(strings.Repeat("[", sec.depth))
			b.WriteString(sec.name)
			b.WriteString(strings.Repeat("]", sec.depth))
			b.WriteByte('\n')
			writeSection(b, sec)
		}
	}
}

// get returns the value of a direct child key, or "".
func (s *Section) get(key string) string {
	for _, it := range s.items {
		if it.kv != nil && it.kv.key == key {
			return it.kv.value
		}
	}
	return ""
}

// subSections returns the direct child sections in order.
func (s *Section) subSections() []*Section {
	var out []*Section
	for _, it := range s.items {
		if it.section != nil {
			out = append(out, it.section)
		}
	}
	return out
}

// Extract reads the CP-managed subset out of an existing config.ini — the
// inverse of Merge. Used to SEED CP from each device's captured config so the
// first CP-driven push doesn't clobber hand-tuned values (issue #5). camera_id
// is the first [cameras] subsection name; a webhook is Enabled if it appears in
// [cameras].webhook_targets.
func Extract(data []byte) (prconfig.Config, error) {
	doc, err := Parse(data)
	if err != nil {
		return prconfig.Config{}, err
	}
	var cfg prconfig.Config
	cameras := doc.root.sub("cameras")
	if cameras == nil {
		return cfg, nil
	}
	cfg.Region = cameras.get("regions")
	if cams := cameras.subSections(); len(cams) > 0 {
		cfg.CameraID = cams[0].name
	}

	enabled := map[string]bool{}
	for _, name := range strings.Split(cameras.get("webhook_targets"), ",") {
		if n := strings.TrimSpace(name); n != "" {
			enabled[n] = true
		}
	}
	if webhooks := doc.root.sub("webhooks"); webhooks != nil {
		for _, wh := range webhooks.subSections() {
			cfg.Webhooks = append(cfg.Webhooks, prconfig.Webhook{
				Name:    wh.name,
				URL:     wh.get("url"),
				Enabled: enabled[wh.name],
				Image:   wh.get("image") == "yes",
				Caching: wh.get("caching") == "yes",
			})
		}
	}
	return cfg, nil
}

func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// Merge applies the CP-managed fields onto the existing config.ini and returns
// the new bytes. CP-managed: [cameras].regions, the LPR camera's url under
// [cameras][[<camera_id>]], and [cameras].webhook_targets + the [webhooks]
// subsections derived from cfg.Webhooks. Everything else is preserved verbatim,
// including unmodeled per-webhook keys (header, image_quality, …).
func Merge(existing []byte, cfg prconfig.Config, lprURL string) ([]byte, error) {
	doc, err := Parse(existing)
	if err != nil {
		return nil, err
	}
	cameras := doc.root.ensureSub("cameras")
	if cfg.Region != "" {
		cameras.setKV("regions", cfg.Region)
	}

	// LPR camera: set the url (+ active) under [cameras][[<camera_id>]],
	// preserving any other keys on an existing camera subsection.
	if cfg.CameraID != "" {
		cam := cameras.ensureSub(cfg.CameraID)
		if lprURL != "" {
			cam.setKV("url", lprURL)
		}
		cam.setKV("active", "yes")
	}

	// Webhooks: webhook_targets lists the enabled ones; each [[name]] subsection
	// gets url/name/image/caching (preserving header/image_quality/etc.).
	var enabled []string
	webhooks := doc.root.ensureSub("webhooks")
	for _, wh := range cfg.Webhooks {
		if wh.Name == "" {
			continue
		}
		sub := webhooks.ensureSub(wh.Name)
		sub.setKV("url", wh.URL)
		sub.setKV("name", wh.Name)
		sub.setKV("image", boolWord(wh.Image))
		sub.setKV("caching", boolWord(wh.Caching))
		if wh.Enabled {
			enabled = append(enabled, wh.Name)
		}
	}
	cameras.setKV("webhook_targets", strings.Join(enabled, ", "))

	return doc.Bytes(), nil
}
