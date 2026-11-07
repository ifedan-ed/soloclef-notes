package executor

import (
	"testing"
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/broker"
)

func TestTheDefaultsAreTheLimitsCeleryAlreadyEnforces(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HardTimeLimit != broker.CeleryHardTimeLimit || cfg.SoftTimeLimit != broker.CelerySoftTimeLimit {
		t.Errorf("limits = %v / %v, want app/celery_app.py's own", cfg.SoftTimeLimit, cfg.HardTimeLimit)
	}
	if len(cfg.Command) != 0 {
		t.Error("a default config must name no command: a worker runs what it was told to run")
	}
}

func TestAConfigThatCouldNotRunOrReportIsRefused(t *testing.T) {
	sound := func() Config {
		cfg := DefaultConfig()
		cfg.Command = []string{"python", "-m", "app.tasks.run"}
		cfg.Worker = "go-worker@host"
		return cfg
	}
	if err := sound().Validate(); err != nil {
		t.Fatalf("a sound config was refused: %v", err)
	}

	cases := map[string]func(*Config){
		"no command":           func(c *Config) { c.Command = nil },
		"no worker name":       func(c *Config) { c.Worker = "" },
		"no hard limit":        func(c *Config) { c.HardTimeLimit = 0 },
		"negative soft limit":  func(c *Config) { c.SoftTimeLimit = -time.Second },
		"soft limit too late":  func(c *Config) { c.SoftTimeLimit = c.HardTimeLimit },
		"no kill grace":        func(c *Config) { c.KillGrace = 0 },
		"negative start delay": func(c *Config) { c.StartFailureDelay = -time.Second },
	}
	for name, breakIt := range cases {
		cfg := sound()
		breakIt(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestNewRefusesAnExecutorThatCouldNotWriteRows(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Command = []string{"python"}
	cfg.Worker = "go-worker@host"
	if _, err := New(cfg, nil); err == nil {
		t.Error("an executor with no result backend must be refused")
	}
}
