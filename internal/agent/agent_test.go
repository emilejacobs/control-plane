package agent_test

import (
	"strings"
	"testing"

	"github.com/uknomi/control-plane/internal/agent"
)

func TestAgentNewRefusesWithMissingCert(t *testing.T) {
	_, err := agent.New(agent.Config{
		CertPath: "/nonexistent/cert.pem",
	})

	if err == nil {
		t.Fatal("expected error for missing cert, got nil")
	}
	if !strings.Contains(err.Error(), "/nonexistent/cert.pem") {
		t.Errorf("error should name the missing cert path; got: %v", err)
	}
}
