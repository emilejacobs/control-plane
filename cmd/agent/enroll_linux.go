//go:build linux

package main

import (
	"os"
	"strings"

	"github.com/emilejacobs/control-plane/internal/agent/enroll"
)

// gatherHardware reads the Linux (Pi/Radxa) host identity for enrollment
// (ADR-007 minimal agent). Identity sources, all best-effort:
//   - UUID:  /etc/machine-id (systemd-stable), falling back to the device-tree
//            board serial.
//   - kind:  derived from the device-tree model ("pi" / "radxa" / "linux").
//   - OS:    PRETTY_NAME from /etc/os-release.
func gatherHardware() enroll.Hardware {
	host, agentVersion := hostnameAndVersion()
	model := readDeviceTree("model")
	return enroll.Hardware{
		Hostname:     host,
		HardwareUUID: linuxMachineUUID(),
		HardwareKind: linuxKindFromModel(model),
		OSVersion:    parseOSReleaseVersion(readFileQuiet("/etc/os-release")),
		AgentVersion: agentVersion,
	}
}

// linuxMachineUUID prefers /etc/machine-id (stable across reboots, present on
// any systemd host) and falls back to the board serial from the device tree.
func linuxMachineUUID() string {
	if id := strings.TrimSpace(readFileQuiet("/etc/machine-id")); id != "" {
		return id
	}
	return trimDeviceTree(readDeviceTree("serial-number"))
}

// readDeviceTree reads a device-tree property (Pi/Radxa expose model +
// serial-number), tolerating either mount path. Values are NUL-terminated.
func readDeviceTree(prop string) string {
	for _, base := range []string{"/sys/firmware/devicetree/base/", "/proc/device-tree/"} {
		if v := readFileQuiet(base + prop); v != "" {
			return trimDeviceTree(v)
		}
	}
	return ""
}

func trimDeviceTree(s string) string {
	return strings.TrimSpace(strings.TrimRight(s, "\x00"))
}

func readFileQuiet(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
