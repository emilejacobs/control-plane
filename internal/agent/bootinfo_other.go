//go:build !darwin

package agent

// readBootInfo reports ok=false off macOS — the fleet is Macs, and the
// shutdown-cause source is macOS-specific. The boot-info heartbeat fields are
// then omitted entirely.
func readBootInfo() (BootInfo, bool) { return BootInfo{}, false }
