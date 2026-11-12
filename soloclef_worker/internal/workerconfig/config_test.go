package workerconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func complete(t *testing.T) map[string]string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "litellm-key")
	if err := os.WriteFile(key, []byte("sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "session-secret")
	if err := os.WriteFile(secret, []byte("fake-deployment-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		ProxyURL: "http://litellm:4000/v1/", ProxyKeyFile: key, ChatModel: "deepseek-v4.1", QuickModel: "deepseek-v4.1-flash",
		SecretKeyFile: secret,
		CallTimeout:   "5m", SpeechService: "openai/gpt-4o-mini-tts", SpeechAttemptTimeout: "90s",
		SpeechMaxAttempts: "3", SpeechConcurrency: "8", PodcastWorkDirectory: "/tmp/podcasts",
		QdrantURL: "http://qdrant:6333", QdrantAlias: "chunks", QdrantTimeout: "30s",
		FileStorageBackend: "local", FileStorageLocalPath: "/data", DefaultQueue: "soloclef",
	}
}

func TestACompleteEnvironmentIsRead(t *testing.T) {
	settings := complete(t)
	config, err := Load(func(name string) string { return settings[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.ProxyURL != "http://litellm:4000/v1" || config.ProxyKey != "sk-test" || config.CallTimeout != 5*time.Minute ||
		config.Speech.Model != "gpt-4o-mini-tts" || config.SpeechConcurrency != 8 || config.SpeechInstructions != "" ||
		config.DefaultQueue != "soloclef" || config.SecretKey != "fake-deployment-secret" {
		t.Fatalf("read %+v", config)
	}
}

// Every problem is named at once, so a deployment missing five settings is
// told about five.
func TestEverythingMissingIsNamedAtOnce(t *testing.T) {
	settings := complete(t)
	delete(settings, QuickModel)
	delete(settings, QdrantAlias)
	delete(settings, DefaultQueue)
	// Without it an account's own model could not be opened.
	delete(settings, SecretKeyFile)
	settings[SpeechService] = "local/kokoro"
	settings[SpeechConcurrency] = "0"
	_, err := Load(func(name string) string { return settings[name] })
	if err == nil {
		t.Fatal("an incomplete environment was accepted")
	}
	for _, named := range []string{QuickModel, QdrantAlias, DefaultQueue, SecretKeyFile, SpeechService, SpeechConcurrency} {
		if !strings.Contains(err.Error(), named) {
			t.Errorf("%s is not named in %v", named, err)
		}
	}
}
