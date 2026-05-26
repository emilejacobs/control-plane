package networkscan_test

import (
	"testing"

	"github.com/emilejacobs/control-plane/internal/handlers/networkscan"
)

// LookupVendor returns the vendor string for a given MAC's OUI prefix.
// We seed the embedded table with the camera brands the spec calls out:
// Hikvision, Dahua, Axis, Reolink, Amcrest, Uniview, Vivotek, Hanwha.
// Unknown OUIs return "" (caller renders empty cell).
func TestLookupVendor(t *testing.T) {
	cases := []struct {
		name string
		mac  string
		want string
	}{
		// Common Hikvision OUI 44:19:b6.
		{"hikvision lowercase", "44:19:b6:aa:bb:cc", "Hikvision"},
		// Case-insensitive match — MACs from arp-scan come uppercase
		// or mixed; the lookup canonicalises before comparing.
		{"hikvision uppercase", "44:19:B6:AA:BB:CC", "Hikvision"},
		// Dahua OUI 3c:ef:8c.
		{"dahua", "3c:ef:8c:11:22:33", "Dahua"},
		// Axis OUI 00:40:8c.
		{"axis", "00:40:8c:de:ad:be", "Axis"},
		// Reolink OUI ec:71:db.
		{"reolink", "ec:71:db:55:66:77", "Reolink"},
		// Unknown OUI → empty string.
		{"unknown", "12:34:56:78:9a:bc", ""},
		// Garbage MAC → empty string (lookup never panics).
		{"empty mac", "", ""},
		{"too short", "44:19", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := networkscan.LookupVendor(tc.mac)
			if got != tc.want {
				t.Errorf("LookupVendor(%q): got %q, want %q", tc.mac, got, tc.want)
			}
		})
	}
}
