package pushdelivery

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_notify/apns"
	"git.danvics.com/danvics/soloclef/soloclef_notify/apns/apnstest"
)

// Apple's five settings are set together or not at all: none is a worker
// with no Apple key (iPhones on APNs tokens are not pushed), some is a
// mistake that names what is missing, and the environment is exactly
// production or sandbox.
func TestTheAppleSettingsAreAllOrNothing(t *testing.T) {
	base := map[string]string{Enabled: "true", PublicURL: "https://notes.example.com"}
	apple := map[string]string{
		APNsKeyFile: "/run/secrets/apns_key", APNsKeyID: "KEYID12345", APNsTeamID: "TEAMID1234",
		APNsTopic: "com.danvics.soloclef", APNsEnvironment: "production",
	}
	with := func(over map[string]string) func(string) string {
		values := map[string]string{}
		for k, v := range base {
			values[k] = v
		}
		for k, v := range over {
			values[k] = v
		}
		return func(k string) string { return values[k] }
	}
	if c, err := LoadConfig(with(nil)); err != nil || c.APNs.Set() {
		t.Errorf("none set: %+v %v", c.APNs, err)
	}
	c, err := LoadConfig(with(apple))
	if err != nil || !c.APNs.Set() || c.APNs != (APNsConfig{KeyFile: "/run/secrets/apns_key", KeyID: "KEYID12345", TeamID: "TEAMID1234", Topic: "com.danvics.soloclef", Environment: "production"}) {
		t.Errorf("all set: %+v %v", c.APNs, err)
	}
	for name := range apple {
		partial := map[string]string{}
		for k, v := range apple {
			partial[k] = v
		}
		partial[name] = ""
		if _, err := LoadConfig(with(partial)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("without %s: %v", name, err)
		}
	}
	for _, environment := range []string{"Production", "development", "prod"} {
		bad := map[string]string{}
		for k, v := range apple {
			bad[k] = v
		}
		bad[APNsEnvironment] = environment
		if _, err := LoadConfig(with(bad)); err == nil {
			t.Errorf("the environment %q was accepted", environment)
		}
	}
	// Push off: Apple's settings are not read at all.
	if c, err := LoadConfig(func(k string) string { return map[string]string{Enabled: "false", APNsKeyFile: "x"}[k] }); err != nil || c.APNs.Set() {
		t.Errorf("push off: %+v %v", c, err)
	}
}

// The key is read and checked when the worker starts: a file that is not
// there, or not Apple's kind of key, stops it there, naming the setting.
func TestTheAppleKeyIsReadAtStart(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	good := filepath.Join(dir, "AuthKey_TESTKEY001.p8")
	if err := os.WriteFile(good, apnstest.KeyPEM(t), 0o444); err != nil {
		t.Fatal(err)
	}
	notAKey := filepath.Join(dir, "not-a-key")
	if err := os.WriteFile(notAKey, []byte("hello"), 0o444); err != nil {
		t.Fatal(err)
	}
	config := APNsConfig{KeyFile: good, KeyID: apnstest.KeyID, TeamID: apnstest.TeamID, Topic: apnstest.Topic, Environment: apns.Sandbox}
	client, err := APNsClient(config, log)
	if err != nil || client.BaseURL != apns.SandboxURL || client.Topic != apnstest.Topic {
		t.Fatalf("a good key: %+v %v", client, err)
	}
	for name, broken := range map[string]APNsConfig{
		"a missing file": {KeyFile: filepath.Join(dir, "absent.p8"), KeyID: apnstest.KeyID, TeamID: apnstest.TeamID, Topic: apnstest.Topic, Environment: apns.Production},
		"not a key":      {KeyFile: notAKey, KeyID: apnstest.KeyID, TeamID: apnstest.TeamID, Topic: apnstest.Topic, Environment: apns.Production},
		"a bad key id":   {KeyFile: good, KeyID: "short", TeamID: apnstest.TeamID, Topic: apnstest.Topic, Environment: apns.Production},
	} {
		if _, err := APNsClient(broken, log); err == nil || !strings.Contains(err.Error(), APNsKeyFile) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
