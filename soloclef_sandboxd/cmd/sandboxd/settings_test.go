package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fullSettings() map[string]string {
	return map[string]string{
		"SOLOCLEF_SANDBOXD_STACK":         "soloclefgo",
		"SOLOCLEF_SANDBOXD_IMAGE":         "soloclefgo-sandbox:local",
		"SOLOCLEF_SANDBOXD_PATH":          "/usr/bin:/bin",
		"SOLOCLEF_SANDBOXD_USER":          "1000:1000",
		"SOLOCLEF_SANDBOXD_LISTEN":        ":8080",
		"SOLOCLEF_SANDBOXD_DOCKER_SOCKET": "/var/run/docker.sock",
		"SOLOCLEF_SANDBOXD_KEY_FILE":      "/run/secrets/sandbox_api_key",
		"SOLOCLEF_SANDBOXD_KEY_COPY":      "/run/sandbox-key/api_key",
	}
}

func TestEverySettingIsRequired(t *testing.T) {
	for name := range fullSettings() {
		values := fullSettings()
		delete(values, name)
		if _, err := load(func(n string) string { return values[n] }); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("without %s: %v", name, err)
		}
	}
	values := fullSettings()
	values["SOLOCLEF_SANDBOXD_LISTEN"] = "0.0.0.0:8080"
	if _, err := load(func(n string) string { return values[n] }); err == nil {
		t.Error("a listen address with a host was accepted; the health probe dials 127.0.0.1 on the port")
	}
	if _, err := load(func(n string) string { return fullSettings()[n] }); err != nil {
		t.Errorf("full settings: %v", err)
	}
}

func TestTheKeyIsReadAndPublished(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	if err := os.WriteFile(keyFile, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readKey(keyFile); err == nil {
		t.Error("a short key was accepted")
	}
	long := strings.Repeat("ab", 32)
	if err := os.WriteFile(keyFile, []byte(long+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := readKey(keyFile)
	if err != nil || key != long {
		t.Fatalf("read %q (%v)", key, err)
	}

	// A read-only copy a crashed start left beside the destination is
	// replaced, not written into.
	published := filepath.Join(dir, "api_key")
	if err := os.WriteFile(filepath.Join(dir, ".api_key.new"), []byte("stale"), 0o444); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := publishKey(key, published); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(published)
	info, _ := os.Stat(published)
	if err != nil || string(data) != long+"\n" || info.Mode().Perm() != 0o444 {
		t.Errorf("published %q, mode %v (%v)", data, info.Mode(), err)
	}
}
