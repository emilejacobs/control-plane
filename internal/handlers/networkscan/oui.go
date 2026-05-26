package networkscan

import (
	"bufio"
	"bytes"
	_ "embed"
	"strings"
	"sync"
)

// ouiTableCSV is the curated MAC-prefix → vendor table embedded into
// the agent binary. Scope (per ADR-030 § 2 + issue #3): the common
// camera brands the operator deploys against — Hikvision, Dahua, Axis,
// Reolink, Amcrest, Uniview, Vivotek, Hanwha. Not the full IEEE OUI
// registry; bundling all ~30K OUIs would 10× the agent binary for
// vendor strings nobody on a store LAN cares about.
//
// Adding a brand: append `prefix,vendor` rows; lowercase the prefix in
// xx:xx:xx form. The init parser reads colon-delimited prefixes and
// canonicalises caller-supplied MACs to match.
//
//go:embed oui_table.csv
var ouiTableCSV []byte

var (
	ouiOnce sync.Once
	ouiMap  map[string]string
)

func loadOUITable() {
	ouiMap = make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(ouiTableCSV))
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if first {
			first = false
			// Skip CSV header row.
			if strings.HasPrefix(strings.ToLower(line), "prefix,") {
				continue
			}
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}
		prefix := strings.ToLower(strings.TrimSpace(parts[0]))
		vendor := strings.TrimSpace(parts[1])
		if prefix == "" || vendor == "" {
			continue
		}
		ouiMap[prefix] = vendor
	}
}

// LookupVendor returns the vendor string for a MAC's OUI prefix. The
// MAC is canonicalised to lowercase xx:xx:xx form before lookup. An
// unknown OUI (or any malformed input) returns "" — callers render an
// empty vendor cell rather than crashing.
func LookupVendor(mac string) string {
	ouiOnce.Do(loadOUITable)
	prefix, ok := ouiPrefix(mac)
	if !ok {
		return ""
	}
	return ouiMap[prefix]
}

// ouiPrefix extracts the first three octets of a MAC in lowercase
// xx:xx:xx form. Returns false if the input doesn't have at least
// three colon-separated octets.
func ouiPrefix(mac string) (string, bool) {
	if len(mac) < 8 { // xx:xx:xx minimum
		return "", false
	}
	parts := strings.Split(strings.ToLower(mac), ":")
	if len(parts) < 3 {
		return "", false
	}
	for _, p := range parts[:3] {
		if len(p) != 2 {
			return "", false
		}
	}
	return parts[0] + ":" + parts[1] + ":" + parts[2], true
}
