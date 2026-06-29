//go:build darwin

package main

import (
	"os/exec"
	"regexp"
	"strings"

	"github.com/emilejacobs/control-plane/internal/agent/enroll"
)

// gatherHardware reads the macOS host identity (ioreg UUID + sw_vers version).
func gatherHardware() enroll.Hardware {
	host, agentVersion := hostnameAndVersion()
	return enroll.Hardware{
		Hostname:     host,
		HardwareUUID: ioregPlatformUUID(),
		HardwareKind: "mac",
		OSVersion:    "macOS " + productVersion(),
		AgentVersion: agentVersion,
	}
}

var ioPlatformUUIDRe = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

func ioregPlatformUUID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	if m := ioPlatformUUIDRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

func productVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
