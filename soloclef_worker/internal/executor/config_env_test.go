package executor

import (
	"testing"
	"time"
)

func TestTheEnvironmentNamesTheCommandAndTheShapeItRunsIn(t *testing.T) {
	cfg, err := LoadConfig(mapEnv(map[string]string{
		EnvTaskCommand:       "python -m app.tasks.serve",
		EnvTaskDir:           "/app",
		EnvPoolSize:          "8",
		EnvMaxJobsPerChild:   "250",
		EnvMaxChildRSS:       "3072",
		EnvChildReadyTimeout: "3m",
		EnvTaskSoftTimeLimit: "7h50m",
		EnvTaskHardTimeLimit: "8h",
		EnvKillGrace:         "25s",
		EnvStartFailureDelay: "10s",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Command) != 3 || cfg.Command[2] != "app.tasks.serve" {
		t.Errorf("command = %q", cfg.Command)
	}
	if cfg.Dir != "/app" || cfg.PoolSize != 8 || cfg.MaxJobsPerChild != 250 {
		t.Errorf("config = %#v", cfg)
	}
	if cfg.MaxChildRSS != 3072<<20 {
		t.Errorf("max child RSS = %d, want the number of MiB given", cfg.MaxChildRSS)
	}
	if cfg.ChildReadyTimeout != 3*time.Minute || cfg.KillGrace != 25*time.Second {
		t.Errorf("durations = %v / %v", cfg.ChildReadyTimeout, cfg.KillGrace)
	}
	if cfg.SoftTimeLimit != 7*time.Hour+50*time.Minute || cfg.HardTimeLimit != 8*time.Hour {
		t.Errorf("limits = %v / %v", cfg.SoftTimeLimit, cfg.HardTimeLimit)
	}
}

// An empty environment leaves the defaults, including no command at all: a
// worker runs what it was told to run and nothing else.
func TestAnEmptyEnvironmentLeavesTheDefaults(t *testing.T) {
	cfg, err := LoadConfig(mapEnv(nil))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Command) != 0 {
		t.Errorf("command = %q, want none", cfg.Command)
	}
	if cfg.MaxJobsPerChild != CeleryMaxJobsPerChild {
		t.Errorf("max jobs per child = %d, want worker_max_tasks_per_child", cfg.MaxJobsPerChild)
	}
	if cfg.PoolSize != 0 {
		t.Errorf("pool size = %d, want the one-shot shape until a pool is asked for", cfg.PoolSize)
	}
}

func TestAnUnreadableSettingIsRefusedRatherThanIgnored(t *testing.T) {
	for name, value := range map[string]string{
		EnvPoolSize:          "a few",
		EnvMaxJobsPerChild:   "lots",
		EnvMaxChildRSS:       "2GiB",
		EnvChildReadyTimeout: "soon",
		EnvKillGrace:         "20",
	} {
		if _, err := LoadConfig(mapEnv(map[string]string{name: value})); err == nil {
			t.Errorf("%s=%q was accepted", name, value)
		}
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
