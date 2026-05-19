package agent

import (
	"fmt"
	"os"
)

type Config struct {
	CertPath string
}

type Agent struct{}

func New(cfg Config) (*Agent, error) {
	if _, err := os.Stat(cfg.CertPath); err != nil {
		return nil, fmt.Errorf("cert file %s: %w", cfg.CertPath, err)
	}
	return &Agent{}, nil
}
