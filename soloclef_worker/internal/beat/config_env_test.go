package beat

import (
	"errors"
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

// The Go stack schedules nothing (schedule.go says why), and a beat asked
// to run with nothing to schedule is refused rather than holding a lease
// over nothing.
func TestABeatWithNothingToScheduleIsRefused(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{EnvDefaultQueue: "soloclef"}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Entries) != 0 {
		t.Fatalf("the schedule has %d entries", len(cfg.Entries))
	}
	if err := cfg.Validate(); !errors.Is(err, ErrNoSchedule) {
		t.Fatalf("Validate: %v, want ErrNoSchedule", err)
	}
	if cfg.Origin == "" || cfg.ReplyTo == "" || cfg.Name == "" {
		t.Error("every published envelope needs an origin, a reply_to and a beat name")
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
		{EnvPollInterval: "soon"},
	}
	for _, pairs := range cases {
		if _, err := LoadConfig(env(pairs)); err == nil {
			t.Errorf("LoadConfig(%v) should have failed", pairs)
		}
	}
}
