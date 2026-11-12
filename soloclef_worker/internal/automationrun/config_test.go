package automationrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// secretFile is a file holding a secret, as a deployment mounts one.
func secretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func complete(t *testing.T) map[string]string {
	return map[string]string{
		Enabled: "true", AppRedisURL: "redis://valkey:6379/0",
		EmbeddingModel: "embed", EmbeddingDimension: "1024",
		QdrantCollection: "chunks", QdrantHNSWEf: "128", QdrantQuantized: "false",
		NCBITool: "soloclef", Brand: "Soloclef Notes", MaxSteps: "40",
		SecretKeyFile: secretFile(t, "fake-secret-key"), SandboxEnabled: "false",
	}
}

func TestTheSwitchIsAlwaysRequiredAndTheRestOnlyWhenItIsOn(t *testing.T) {
	if _, err := LoadConfig(env(nil)); err == nil || !strings.Contains(err.Error(), Enabled) {
		t.Errorf("an unset switch: %v", err)
	}
	// Off, nothing else is read: a deployment without automations sets one
	// variable.
	if cfg, err := LoadConfig(env(map[string]string{Enabled: "false"})); err != nil || cfg.Enabled {
		t.Errorf("off: %+v %v", cfg, err)
	}
	cfg, err := LoadConfig(env(complete(t)))
	if err != nil || !cfg.Enabled || cfg.EmbeddingDimension != 1024 || cfg.QdrantHNSWEf != 128 || cfg.MaxSteps != 40 || cfg.NCBIEmail != "" {
		t.Errorf("on: %+v %v", cfg, err)
	}
}

// Everything missing is reported at once, by name, and nothing is given a
// default.
func TestEveryMissingSettingIsNamedAtOnce(t *testing.T) {
	_, err := LoadConfig(env(map[string]string{Enabled: "true", QdrantQuantized: "true", EmbeddingDimension: "many"}))
	if err == nil {
		t.Fatal("an incomplete configuration was accepted")
	}
	for _, name := range []string{AppRedisURL, EmbeddingModel, EmbeddingDimension, QdrantCollection, QdrantHNSWEf, QdrantRescore, QdrantOversampling, NCBITool, Brand, MaxSteps, SecretKeyFile, SandboxEnabled} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not name %s: %v", name, err)
		}
	}
	// How a search uses quantized vectors is asked only of a collection
	// that holds them.
	settings := complete(t)
	settings[QdrantQuantized], settings[QdrantRescore], settings[QdrantOversampling] = "true", "true", "2.5"
	if cfg, err := LoadConfig(env(settings)); err != nil || !cfg.QdrantRescore || cfg.QdrantOversampling != 2.5 {
		t.Errorf("quantized: %+v %v", cfg, err)
	}
}

// What a run reads an account's mail and Drive with, and makes artifacts
// with: each optional group is whole or absent, never half.
func TestTheConnectedAccountsAndArtifactSettings(t *testing.T) {
	settings := complete(t)
	cfg, err := LoadConfig(env(settings))
	if err != nil || cfg.SecretKey != "fake-secret-key" || cfg.MicrosoftMailClientID != "" || cfg.GoogleClientID != "" ||
		cfg.NextcloudURL != "" || cfg.SandboxEnabled || cfg.ImageModel != "" || len(cfg.PodcastVoices) != 0 {
		t.Fatalf("the least a deployment may say: %+v %v", cfg, err)
	}

	settings[MicrosoftMailClientID], settings[MicrosoftMailTenant] = "fake-client", "common"
	settings[MicrosoftMailClientSecretFile] = secretFile(t, "fake-microsoft-secret")
	settings[GoogleClientID], settings[GoogleClientSecretFile] = "fake.apps.googleusercontent.com", secretFile(t, "fake-google-secret")
	settings[NextcloudURL] = "https://cloud.example/nc"
	settings[ImageModel], settings[VisionModel], settings[PodcastVoices] = "image-model", "vision-model", " af_heart, am_adam ,"
	settings[SandboxEnabled], settings[SandboxURL], settings[SandboxAPIKeyFile] = "true", "http://sandbox:8080/", secretFile(t, "fake-sandbox-key")
	settings[SandboxImage], settings[SandboxCPU], settings[SandboxMemory] = "sandbox:latest", "2", "6Gi"
	settings[SandboxIdleTTL], settings[SandboxCommandTimeout], settings[SandboxMaxPerAccount] = "600", "120", "3"
	settings[SandboxStack] = "examplestack"
	cfg, err = LoadConfig(env(settings))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MicrosoftMailClientSecret != "fake-microsoft-secret" || cfg.MicrosoftMailTenant != "common" || cfg.GoogleClientSecret != "fake-google-secret" ||
		cfg.NextcloudURL != "https://cloud.example/nc" || strings.Join(cfg.PodcastVoices, "|") != "af_heart|am_adam" || !cfg.SandboxEnabled {
		t.Errorf("read as %+v", cfg)
	}
	if box := cfg.Sandbox; box.URL != "http://sandbox:8080" || box.APIKey != "fake-sandbox-key" || box.IdleTTL != 10*time.Minute ||
		box.CommandTimeout != 2*time.Minute || box.MaxPerAccount != 3 || box.Image != "sandbox:latest" || box.Stack != "examplestack" {
		t.Errorf("the sandbox read as %+v", box)
	}

	for name, change := range map[string]func(map[string]string){
		"half a Microsoft registration":       func(s map[string]string) { delete(s, MicrosoftMailTenant) },
		"half a Google project":               func(s map[string]string) { delete(s, GoogleClientSecretFile) },
		"a Nextcloud that is not an address":  func(s map[string]string) { s[NextcloudURL] = "cloud.example" },
		"a sandbox with no address":           func(s map[string]string) { delete(s, SandboxURL) },
		"a sandbox with no stack":             func(s map[string]string) { delete(s, SandboxStack) },
		"a secret key file that is not there": func(s map[string]string) { s[SecretKeyFile] = "/nonexistent/secret" },
		"an empty secret key file":            func(s map[string]string) { s[SecretKeyFile] = secretFile(t, "") },
	} {
		changed := map[string]string{}
		for key, value := range settings {
			changed[key] = value
		}
		change(changed)
		if _, err := LoadConfig(env(changed)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
