package beat

import (
	"testing"
	"time"
)

func env(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

// Beat is off unless it is asked for, because two schedulers double every
// scheduled task: a worker started with no configuration schedules nothing.
func TestBeatIsOffUnlessItIsAskedFor(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"false": false,
		"0":     false,
		"no":    false,
		"true":  true,
		"TRUE":  true,
		"1":     true,
		"yes":   true,
	}
	for value, want := range cases {
		if got := Enabled(env(map[string]string{EnvEnabled: value})); got != want {
			t.Errorf("Enabled(%q) = %v, want %v", value, got, want)
		}
	}
}

// The schedule comes from the two settings the Python beat reads, so the two
// schedulers cannot disagree about the cadence or the queue.
func TestLoadConfigTakesTheScheduleFromCelerysOwnSettings(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		EnvDefaultQueue:    "soloclef",
		EnvCheckerInterval: "5m",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, entry := range cfg.Entries {
		if entry.Queue != "soloclef" {
			t.Errorf("%s publishes to %q, not the configured default queue", entry.Name, entry.Queue)
		}
		if entry.Name != "check-periodic-connector-schedules" {
			continue
		}
		if entry.Cron.Minute != "*/5" {
			t.Errorf("the checker's minute is %q, not every five", entry.Cron.Minute)
		}
	}
	if cfg.Origin == "" || cfg.ReplyTo == "" {
		t.Error("every published envelope needs an origin and a reply_to")
	}
	if cfg.Name == "" {
		t.Error("a beat with no name cannot be told apart in the lease")
	}
}

// Unset mirrors what app/config reads when the variable is unset, so a worker
// with neither set schedules what the Python beat would schedule.
func TestLoadConfigDefaultsToWhatThePythonSideDefaultsTo(t *testing.T) {
	cfg, err := LoadConfig(env(nil))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, entry := range cfg.Entries {
		if entry.Name != "check-periodic-connector-schedules" {
			continue
		}
		// SCHEDULE_CHECKER_INTERVAL defaults to "2m" in app/config.
		if entry.Cron.Minute != "*/2" {
			t.Errorf("the checker's minute is %q, want */2", entry.Cron.Minute)
		}
	}
	if cfg.Entries[0].Queue != "surfsense" {
		t.Errorf("the default queue is %q, want surfsense", cfg.Entries[0].Queue)
	}
}

func TestLoadConfigReadsTheDurations(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		EnvKeyPrefix:    "probe",
		EnvLeaseTTL:     "2m",
		EnvLeaseRenew:   "40s",
		EnvLeaseRetry:   "3s",
		EnvFenceTTL:     "30m",
		EnvPollInterval: "2s",
		EnvName:         "beat-one",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.KeyPrefix != "probe" || cfg.Name != "beat-one" {
		t.Errorf("prefix %q, name %q", cfg.KeyPrefix, cfg.Name)
	}
	if cfg.LeaseTTL != 2*time.Minute || cfg.LeaseRenew != 40*time.Second ||
		cfg.LeaseRetry != 3*time.Second || cfg.FenceTTL != 30*time.Minute ||
		cfg.PollInterval != 2*time.Second {
		t.Errorf("durations = %+v", cfg)
	}
}

func TestLoadConfigRefusesWhatItCannotRead(t *testing.T) {
	cases := []map[string]string{
		{EnvLeaseTTL: "forever"},
		{EnvCheckerInterval: "xm"},
	}
	for _, pairs := range cases {
		if _, err := LoadConfig(env(pairs)); err == nil {
			t.Errorf("LoadConfig(%v) should have failed", pairs)
		}
	}
}
