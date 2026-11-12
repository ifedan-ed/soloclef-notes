package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// proxyKeyFile is the file the complete environment names as the model
// proxy's key. It is one file for the whole package, written by TestMain,
// because every test starts from completeEnvironment and the setting is
// required: a test that wants another file, or none, overrides the variable.
var proxyKeyFile string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "soloclef-api-config")
	if err != nil {
		panic(err)
	}
	proxyKeyFile = filepath.Join(directory, "litellm-key")
	// Ending in a newline, the way `echo` writes a secret.
	if err := os.WriteFile(proxyKeyFile, []byte("sk-example-proxy-key\n"), 0o600); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func completeEnvironment() map[string]string {
	return map[string]string{
		// The chat's settings: all required, none defaulted.
		"SOLOCLEF_API_LITELLM_URL":                "http://litellm.example.internal:4000/v1",
		"SOLOCLEF_API_LITELLM_KEY_FILE":           proxyKeyFile,
		"SOLOCLEF_API_CHAT_MODEL":                 "example-chat-model",
		"SOLOCLEF_API_EMBEDDING_MODEL":            "example-embedding-model",
		"SOLOCLEF_API_EMBEDDING_DIMENSION":        "1024",
		"SOLOCLEF_API_QDRANT_URL":                 "http://qdrant.example.internal:6333",
		"SOLOCLEF_API_QDRANT_CHUNK_COLLECTION":    "chunks",
		"SOLOCLEF_API_QDRANT_SEARCH_HNSW_EF":      "128",
		"SOLOCLEF_API_QDRANT_QUANTIZED":           "true",
		"SOLOCLEF_API_QDRANT_SEARCH_RESCORE":      "true",
		"SOLOCLEF_API_QDRANT_SEARCH_OVERSAMPLING": "2.5",
		"SOLOCLEF_API_NCBI_TOOL":                  "example-notes",
		"SOLOCLEF_API_CHAT_MAX_STEPS":             "24",
		"SOLOCLEF_API_CHAT_MAX_TURN_SECONDS":      "600",
		// Whether documents are built in a sandbox: said, never assumed.
		"SOLOCLEF_API_SANDBOX_ENABLED": "false",
		// Who may make an account, and the two things that are switched on
		// or off whole: each said, never assumed.
		"SOLOCLEF_API_SIGNUP_ALLOWED": "false",
		"SOLOCLEF_API_OIDC_ENABLED":   "false",
		"SOLOCLEF_API_SMTP_ENABLED":   "false",
		// The end-to-end test routes: said, never assumed.
		"SOLOCLEF_API_E2E_ROUTES": "false",
		// The uploads' limits: required, none defaulted.
		"SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES": "524288000",
		"SOLOCLEF_API_UPLOAD_MAX_FILES":      "10",
		// How long a note's indexing waits: required, not defaulted.
		"SOLOCLEF_API_NOTE_INDEX_DELAY_SECONDS": "15",

		"SOLOCLEF_API_LISTEN_ADDRESS":   "127.0.0.1:8081",
		"SOLOCLEF_API_DATABASE_URL":     "postgres://user:pw@db:5432/notes",
		"SOLOCLEF_API_SESSION_SECRET":   "test-secret",
		"SOLOCLEF_API_ERROR_REPORT_URL": "https://notes.example.com/contact",
		"SOLOCLEF_API_REDIS_URL":        "redis://redis:6379/0",
		// The broker is a setting of its own: the same server as the app Redis
		// on this host, and two settings in app/config either way.
		"SOLOCLEF_API_CELERY_BROKER_URL":       "redis://redis:6379/0",
		"SOLOCLEF_API_CELERY_DEFAULT_QUEUE":    "surfsense",
		"SOLOCLEF_API_FILE_STORAGE_BACKEND":    "local",
		"SOLOCLEF_API_FILE_STORAGE_LOCAL_PATH": "/srv/objects",
	}
}

func TestLoadMirrorsThePythonTiersDefaults(t *testing.T) {
	config, err := Load(fakeEnvironment(completeEnvironment()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.SessionCookieName != "surfsense_session" {
		t.Errorf("cookie name: got %q", config.SessionCookieName)
	}
	if config.MinIssuedAt != 0 {
		t.Errorf("min issued at: got %d", config.MinIssuedAt)
	}
	if config.DatabaseMaxConnections != 10 {
		t.Errorf("max connections: got %d", config.DatabaseMaxConnections)
	}
	if config.ShutdownGrace != 15*time.Second {
		t.Errorf("shutdown grace: got %s", config.ShutdownGrace)
	}
	if config.AllowedOrigins != nil {
		t.Errorf("allowed origins: got %v, want none", config.AllowedOrigins)
	}
	if config.AgentProviderName != "authentik" {
		t.Errorf("agent provider name: got %q", config.AgentProviderName)
	}
	if config.BrandName != "Soloclef Notes" {
		t.Errorf("brand name: got %q", config.BrandName)
	}
	if config.PostHogAPIKey != "" || config.PostHogHost != "" {
		t.Errorf("posthog: got %q/%q, want telemetry off", config.PostHogAPIKey, config.PostHogHost)
	}
	if config.AgentIssuer != "" || config.AgentClientID != "" {
		t.Errorf("agent provider: got %q/%q, want none configured",
			config.AgentIssuer, config.AgentClientID)
	}
}

// A project key with nowhere to send to would make this tier guess a vendor's
// address, which is the one thing no setting here does.
func TestLoadRefusesAProjectKeyWithNoHost(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_POSTHOG_API_KEY"] = "phc_a_project_key"
	if _, err := Load(fakeEnvironment(values)); err == nil {
		t.Error("want an error, got none")
	}
	values["SOLOCLEF_API_POSTHOG_HOST"] = "https://telemetry.example.invalid/"
	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.PostHogHost != "https://telemetry.example.invalid" {
		t.Errorf("host: got %q, want it without its trailing slash", config.PostHogHost)
	}
}

// Half of the agent provider's settings would leave the audience check — the
// one thing that stops a token issued to another application being used here —
// with nothing to check against.
func TestLoadRefusesHalfOfTheAgentProvider(t *testing.T) {
	for name, half := range map[string]string{
		"only the issuer":    "SOLOCLEF_API_AGENT_ISSUER",
		"only the client id": "SOLOCLEF_API_AGENT_CLIENT_ID",
	} {
		values := completeEnvironment()
		values[half] = "something"
		if _, err := Load(fakeEnvironment(values)); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}

	values := completeEnvironment()
	values["SOLOCLEF_API_AGENT_ISSUER"] = "https://sso.example.com/application/o/agents/"
	values["SOLOCLEF_API_AGENT_CLIENT_ID"] = "agents"
	if _, err := Load(fakeEnvironment(values)); err != nil {
		t.Errorf("both set: %v", err)
	}

	// The client secret is read from its file, and only beside a provider.
	secret := filepath.Join(t.TempDir(), "agent_client_secret")
	if err := os.WriteFile(secret, []byte("shown-to-people\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values["SOLOCLEF_API_AGENT_CLIENT_SECRET_FILE"] = secret
	loaded, err := Load(fakeEnvironment(values))
	if err != nil || loaded.AgentClientSecret != "shown-to-people" {
		t.Errorf("with a secret file: %q, %v", loaded.AgentClientSecret, err)
	}
	alone := completeEnvironment()
	alone["SOLOCLEF_API_AGENT_CLIENT_SECRET_FILE"] = secret
	if _, err := Load(fakeEnvironment(alone)); err == nil {
		t.Error("a secret with no provider: want an error, got none")
	}
}

func TestLoadReadsTheOriginListAsAList(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_ALLOWED_ORIGINS"] = " https://notes.example.com , https://www.notes.example.com ,"

	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"https://notes.example.com", "https://www.notes.example.com"}
	if len(config.AllowedOrigins) != len(want) {
		t.Fatalf("got %v, want %v", config.AllowedOrigins, want)
	}
	for index, origin := range want {
		if config.AllowedOrigins[index] != origin {
			t.Errorf("origin %d: got %q, want %q", index, config.AllowedOrigins[index], origin)
		}
	}
}

func TestLoadReportsEverySettingThatCouldNameAServer(t *testing.T) {
	_, err := Load(fakeEnvironment(map[string]string{}))
	if err == nil {
		t.Fatal("want an error, got none")
	}
	for _, name := range []string{
		"SOLOCLEF_API_LISTEN_ADDRESS",
		"SOLOCLEF_API_DATABASE_URL",
		"SOLOCLEF_API_SESSION_SECRET",
		"SOLOCLEF_API_ERROR_REPORT_URL",
		"SOLOCLEF_API_REDIS_URL",
		"SOLOCLEF_API_CELERY_BROKER_URL",
		"SOLOCLEF_API_CELERY_DEFAULT_QUEUE",
		"SOLOCLEF_API_FILE_STORAGE_BACKEND",
		// The chat reaches a model proxy and a vector store, and is bounded
		// by numbers somebody has to have chosen.
		"SOLOCLEF_API_LITELLM_URL",
		"SOLOCLEF_API_LITELLM_KEY_FILE",
		"SOLOCLEF_API_CHAT_MODEL",
		"SOLOCLEF_API_EMBEDDING_MODEL",
		"SOLOCLEF_API_EMBEDDING_DIMENSION",
		"SOLOCLEF_API_QDRANT_URL",
		"SOLOCLEF_API_QDRANT_CHUNK_COLLECTION",
		"SOLOCLEF_API_QDRANT_SEARCH_HNSW_EF",
		"SOLOCLEF_API_QDRANT_QUANTIZED",
		"SOLOCLEF_API_QDRANT_SEARCH_RESCORE",
		"SOLOCLEF_API_QDRANT_SEARCH_OVERSAMPLING",
		"SOLOCLEF_API_NCBI_TOOL",
		"SOLOCLEF_API_CHAT_MAX_STEPS",
		"SOLOCLEF_API_CHAT_MAX_TURN_SECONDS",
		"SOLOCLEF_API_SANDBOX_ENABLED",
		// What an upload may carry is the operator's decision.
		"SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES",
		"SOLOCLEF_API_UPLOAD_MAX_FILES",
		// How soon a note is searchable is the operator's decision too.
		"SOLOCLEF_API_NOTE_INDEX_DELAY_SECONDS",
	} {
		if !strings.Contains(err.Error(), name+" is required") {
			t.Errorf("%s is not reported as required: %v", name, err)
		}
	}
}

func TestLoadReadsTheChatSettings(t *testing.T) {
	values := completeEnvironment()
	// A trailing slash on either address would double the one a path starts with.
	values["SOLOCLEF_API_LITELLM_URL"] = "http://litellm.example.internal:4000/v1/"
	values["SOLOCLEF_API_QDRANT_URL"] = "http://qdrant.example.internal:6333/"
	values["SOLOCLEF_API_QDRANT_SEARCH_RESCORE"] = "false"

	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.LiteLLMURL != "http://litellm.example.internal:4000/v1" {
		t.Errorf("proxy address: got %q", config.LiteLLMURL)
	}
	// The newline the file ends with is not part of the key.
	if config.LiteLLMKey != "sk-example-proxy-key" {
		t.Errorf("proxy key: got %q", config.LiteLLMKey)
	}
	if config.ChatModel != "example-chat-model" || config.EmbeddingModel != "example-embedding-model" ||
		config.EmbeddingDimension != 1024 {
		t.Errorf("models: got %q, %q at %d", config.ChatModel, config.EmbeddingModel, config.EmbeddingDimension)
	}
	if config.QdrantURL != "http://qdrant.example.internal:6333" || config.QdrantChunkCollection != "chunks" ||
		config.QdrantSearchHNSWEf != 128 {
		t.Errorf("vector store: got %q, %q, ef %d", config.QdrantURL, config.QdrantChunkCollection, config.QdrantSearchHNSWEf)
	}
	if !config.QdrantQuantized || config.QdrantSearchRescore || config.QdrantSearchOversampling != 2.5 {
		t.Errorf("quantization: got quantized %v, rescore %v, oversampling %v",
			config.QdrantQuantized, config.QdrantSearchRescore, config.QdrantSearchOversampling)
	}
	if config.NCBITool != "example-notes" {
		t.Errorf("NCBI tool: got %q", config.NCBITool)
	}
	if config.ChatMaxSteps != 24 || config.ChatMaxTurn != 10*time.Minute {
		t.Errorf("bounds: got %d steps and %s", config.ChatMaxSteps, config.ChatMaxTurn)
	}
	// The three that may be left out are empty, not invented.
	if config.QdrantAPIKey != "" || config.NCBIEmail != "" || config.NCBIAPIKey != "" {
		t.Errorf("optional settings appeared from nowhere: %q, %q, %q",
			config.QdrantAPIKey, config.NCBIEmail, config.NCBIAPIKey)
	}
}

// The three optional chat settings, given: the two keys come from files, as
// every secret this tier is handed for the chat does.
func TestLoadReadsTheOptionalChatSettingsWhenGiven(t *testing.T) {
	directory := t.TempDir()
	qdrantKeyFile := filepath.Join(directory, "qdrant-key")
	ncbiKeyFile := filepath.Join(directory, "ncbi-key")
	for path, content := range map[string]string{qdrantKeyFile: "example-qdrant-key\n", ncbiKeyFile: " example-ncbi-key \n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	values := completeEnvironment()
	values["SOLOCLEF_API_QDRANT_API_KEY_FILE"] = qdrantKeyFile
	values["SOLOCLEF_API_NCBI_API_KEY_FILE"] = ncbiKeyFile
	values["SOLOCLEF_API_NCBI_EMAIL"] = "operator@notes.example"

	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.QdrantAPIKey != "example-qdrant-key" || config.NCBIAPIKey != "example-ncbi-key" ||
		config.NCBIEmail != "operator@notes.example" {
		t.Errorf("got %q, %q, %q", config.QdrantAPIKey, config.NCBIAPIKey, config.NCBIEmail)
	}
}

// A key is given as a file and in no other way: a plain-value variable is
// not read, so the required one is still missing and the optional ones are
// still absent, rather than a pasted key quietly being the one in use.
func TestLoadTakesNoChatKeyAsAPlainValue(t *testing.T) {
	values := completeEnvironment()
	delete(values, "SOLOCLEF_API_LITELLM_KEY_FILE")
	values["SOLOCLEF_API_LITELLM_KEY"] = "pasted-inline"
	_, err := Load(fakeEnvironment(values))
	if err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_LITELLM_KEY_FILE is required") {
		t.Errorf("got %v, want the file variable reported as required", err)
	}

	values = completeEnvironment()
	values["SOLOCLEF_API_QDRANT_API_KEY"] = "pasted-inline"
	values["SOLOCLEF_API_NCBI_API_KEY"] = "pasted-inline"
	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.QdrantAPIKey != "" || config.NCBIAPIKey != "" {
		t.Errorf("a plain-value key was read: %q, %q", config.QdrantAPIKey, config.NCBIAPIKey)
	}
}

// A key file that is named has to be usable, required or not: naming one is
// saying it should be read, and starting without it would be a quiet change
// of what the deployment asked for.
func TestLoadRefusesAChatKeyFileItCannotUse(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(t.TempDir(), "absent")
	for _, name := range []string{
		"SOLOCLEF_API_LITELLM_KEY_FILE", "SOLOCLEF_API_QDRANT_API_KEY_FILE", "SOLOCLEF_API_NCBI_API_KEY_FILE",
	} {
		for path, want := range map[string]string{absent: "which cannot be read", empty: "which is empty"} {
			values := completeEnvironment()
			values[name] = path
			_, err := Load(fakeEnvironment(values))
			if err == nil || !strings.Contains(err.Error(), name+" names ") || !strings.Contains(err.Error(), want) {
				t.Errorf("%s=%s: got %v, want it refused as one %s", name, path, err, want)
			}
		}
	}
}

func TestLoadRefusesUnusableChatValues(t *testing.T) {
	for name, test := range map[string]struct {
		values []string
		want   string
	}{
		"SOLOCLEF_API_EMBEDDING_DIMENSION":   {[]string{"0", "-1", "1024.0", "many"}, "must be a whole number of at least 1"},
		"SOLOCLEF_API_QDRANT_SEARCH_HNSW_EF": {[]string{"0", "wide"}, "must be a whole number of at least 1"},
		"SOLOCLEF_API_CHAT_MAX_STEPS":        {[]string{"0", "unlimited"}, "must be a whole number of at least 1"},
		"SOLOCLEF_API_CHAT_MAX_TURN_SECONDS": {[]string{"0", "10m"}, "must be a whole number of at least 1"},
		// Nothing looser than the two words, as for the bucket's path style.
		"SOLOCLEF_API_QDRANT_QUANTIZED":      {[]string{"1", "yes", "TRUE"}, "must be true or false"},
		"SOLOCLEF_API_QDRANT_SEARCH_RESCORE": {[]string{"0", "no", "False"}, "must be true or false"},
		// Below 1 is refused by Qdrant; NaN and Inf parse as floats and mean nothing.
		"SOLOCLEF_API_QDRANT_SEARCH_OVERSAMPLING": {[]string{"0", "0.5", "NaN", "Inf", "twice"}, "must be a number of at least 1"},
	} {
		for _, value := range test.values {
			values := completeEnvironment()
			values[name] = value
			_, err := Load(fakeEnvironment(values))
			if err == nil || !strings.Contains(err.Error(), name+" "+test.want) {
				t.Errorf("%s=%q: got %v, want it refused", name, value, err)
			}
		}
	}
}

// With the sandbox off nothing about it is read, and the two picture models
// are optional.
func TestLoadReadsNoSandboxWhenItIsOff(t *testing.T) {
	values := completeEnvironment()
	// Written down but switched off: not read, and not a problem.
	values["SOLOCLEF_API_SANDBOX_URL"] = "not even an address"
	values["SOLOCLEF_API_SANDBOX_API_KEY_FILE"] = "/nonexistent/key"
	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.SandboxEnabled || config.SandboxURL != "" || config.SandboxAPIKey != "" ||
		config.ChatImageModel != "" || config.ChatVisionModel != "" {
		t.Errorf("sandbox or picture models appeared from nowhere: %+v", config)
	}

	values = completeEnvironment()
	values["SOLOCLEF_API_CHAT_IMAGE_MODEL"] = "example-image-model"
	values["SOLOCLEF_API_CHAT_VISION_MODEL"] = "example-vision-model"
	if config, err = Load(fakeEnvironment(values)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.ChatImageModel != "example-image-model" || config.ChatVisionModel != "example-vision-model" {
		t.Errorf("picture models: got %q and %q", config.ChatImageModel, config.ChatVisionModel)
	}
}

func sandboxEnvironment(t *testing.T) map[string]string {
	t.Helper()
	keyFile := filepath.Join(t.TempDir(), "sandbox-key")
	if err := os.WriteFile(keyFile, []byte("example-sandbox-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := completeEnvironment()
	for name, value := range map[string]string{
		"SOLOCLEF_API_SANDBOX_ENABLED":                 "true",
		"SOLOCLEF_API_SANDBOX_STACK":                   "examplestack",
		"SOLOCLEF_API_SANDBOX_URL":                     "http://sandbox.example.internal:8080/",
		"SOLOCLEF_API_SANDBOX_API_KEY_FILE":            keyFile,
		"SOLOCLEF_API_SANDBOX_IMAGE":                   "example/sandbox:1",
		"SOLOCLEF_API_SANDBOX_CPU":                     "2",
		"SOLOCLEF_API_SANDBOX_MEMORY":                  "6Gi",
		"SOLOCLEF_API_SANDBOX_IDLE_TTL_SECONDS":        "900",
		"SOLOCLEF_API_SANDBOX_COMMAND_TIMEOUT_SECONDS": "300",
		"SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT":         "3",
	} {
		values[name] = value
	}
	return values
}

func TestLoadReadsTheWholeSandboxWhenItIsOn(t *testing.T) {
	config, err := Load(fakeEnvironment(sandboxEnvironment(t)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !config.SandboxEnabled || config.SandboxStack != "examplestack" || config.SandboxURL != "http://sandbox.example.internal:8080" ||
		config.SandboxAPIKey != "example-sandbox-key" || config.SandboxImage != "example/sandbox:1" ||
		config.SandboxCPU != "2" || config.SandboxMemory != "6Gi" || config.SandboxIdleTTL != 15*time.Minute ||
		config.SandboxCommandTimeout != 5*time.Minute || config.SandboxMaxPerAccount != 3 {
		t.Errorf("sandbox: got %+v", config)
	}

	// Every one of them is required once the switch is on. No vision model
	// is: what is built is measured, not looked at.
	for _, name := range []string{
		"SOLOCLEF_API_SANDBOX_STACK", "SOLOCLEF_API_SANDBOX_URL", "SOLOCLEF_API_SANDBOX_API_KEY_FILE",
		"SOLOCLEF_API_SANDBOX_IMAGE", "SOLOCLEF_API_SANDBOX_CPU", "SOLOCLEF_API_SANDBOX_MEMORY",
		"SOLOCLEF_API_SANDBOX_IDLE_TTL_SECONDS", "SOLOCLEF_API_SANDBOX_COMMAND_TIMEOUT_SECONDS",
		"SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT",
	} {
		values := sandboxEnvironment(t)
		delete(values, name)
		if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), name+" is required") {
			t.Errorf("without %s: got %v, want it reported as required", name, err)
		}
	}
	for name, test := range map[string]struct{ value, want string }{
		"SOLOCLEF_API_SANDBOX_ENABLED":                 {"yes", "must be true or false"},
		"SOLOCLEF_API_SANDBOX_URL":                     {"sandbox.example.internal:8080", "must be an http(s) address"},
		"SOLOCLEF_API_SANDBOX_IDLE_TTL_SECONDS":        {"30", "must be at least 60"},
		"SOLOCLEF_API_SANDBOX_COMMAND_TIMEOUT_SECONDS": {"0", "must be a whole number of at least 1"},
		"SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT":         {"none", "must be a whole number of at least 1"},
	} {
		values := sandboxEnvironment(t)
		values[name] = test.value
		if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), name+" "+test.want) {
			t.Errorf("%s=%q: got %v, want it refused", name, test.value, err)
		}
	}
}

func TestLoadReadsTheUploadLimits(t *testing.T) {
	values := completeEnvironment()
	// Five gigabytes: past what 32 bits hold, and a size a deployment may
	// well choose.
	values["SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES"] = "5368709120"
	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.UploadMaxFileBytes != 5368709120 || config.UploadMaxFiles != 10 {
		t.Errorf("limits: got %d bytes and %d files", config.UploadMaxFileBytes, config.UploadMaxFiles)
	}

	for name, test := range map[string]struct {
		values []string
		want   string
	}{
		"SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES": {[]string{"0", "-1", "500MB", "1e9"}, "must be a whole number of bytes, at least 1"},
		"SOLOCLEF_API_UPLOAD_MAX_FILES":      {[]string{"0", "many"}, "must be a whole number of at least 1"},
		// Zero would index on every autosave, which is what the delay is for.
		"SOLOCLEF_API_NOTE_INDEX_DELAY_SECONDS": {[]string{"0", "-5", "15s", "soon"}, "must be a whole number of at least 1"},
	} {
		for _, value := range test.values {
			values := completeEnvironment()
			values[name] = value
			if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), name+" "+test.want) {
				t.Errorf("%s=%q: got %v, want it refused", name, value, err)
			}
		}
	}
}

// Nextcloud is optional as a whole: no server, no setting, no routes. With a
// server named, how many files are fetched at once has to be said too.
func TestLoadReadsNextcloudWholeOrNotAtAll(t *testing.T) {
	config, err := Load(fakeEnvironment(completeEnvironment()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.NextcloudBaseURL != "" || config.NextcloudDownloadConcurrency != 0 {
		t.Errorf("Nextcloud appeared from nowhere: %q, %d", config.NextcloudBaseURL, config.NextcloudDownloadConcurrency)
	}

	values := completeEnvironment()
	values["SOLOCLEF_API_NEXTCLOUD_BASE_URL"] = "https://cloud.example.com/"
	values["SOLOCLEF_API_NEXTCLOUD_DOWNLOAD_CONCURRENCY"] = "4"
	if config, err = Load(fakeEnvironment(values)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.NextcloudBaseURL != "https://cloud.example.com" || config.NextcloudDownloadConcurrency != 4 {
		t.Errorf("Nextcloud: got %q at %d", config.NextcloudBaseURL, config.NextcloudDownloadConcurrency)
	}

	for name, test := range map[string]struct {
		server, concurrency, want string
	}{
		"a server with no concurrency": {"https://cloud.example.com", "", "SOLOCLEF_API_NEXTCLOUD_DOWNLOAD_CONCURRENCY is required"},
		"a concurrency of nothing":     {"https://cloud.example.com", "0", "SOLOCLEF_API_NEXTCLOUD_DOWNLOAD_CONCURRENCY must be a whole number of at least 1"},
		"a concurrency with no server": {"", "4", "SOLOCLEF_API_NEXTCLOUD_DOWNLOAD_CONCURRENCY is set and SOLOCLEF_API_NEXTCLOUD_BASE_URL is not"},
		"an address that is not a URL": {"cloud.example.com", "4", "SOLOCLEF_API_NEXTCLOUD_BASE_URL must be a plain http(s) address"},
		"an address carrying a login":  {"https://someone:secret@cloud.example.com", "4", "SOLOCLEF_API_NEXTCLOUD_BASE_URL must be a plain http(s) address"},
		"an address carrying a query":  {"https://cloud.example.com/?dir=/", "4", "SOLOCLEF_API_NEXTCLOUD_BASE_URL must be a plain http(s) address"},
		"an address of another scheme": {"ftp://cloud.example.com", "4", "SOLOCLEF_API_NEXTCLOUD_BASE_URL must be a plain http(s) address"},
	} {
		values := completeEnvironment()
		values["SOLOCLEF_API_NEXTCLOUD_BASE_URL"] = test.server
		values["SOLOCLEF_API_NEXTCLOUD_DOWNLOAD_CONCURRENCY"] = test.concurrency
		if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: got %v, want %q", name, err, test.want)
		}
	}
}

func TestLoadRefusesUnusableValues(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_MIN_ISSUED_AT"] = "yesterday"
	values["SOLOCLEF_API_LOG_LEVEL"] = "chatty"
	values["SOLOCLEF_API_DATABASE_MAX_CONNECTIONS"] = "0"

	_, err := Load(fakeEnvironment(values))
	if err == nil {
		t.Fatal("want an error, got none")
	}
	for _, fragment := range []string{"MIN_ISSUED_AT", "LOG_LEVEL", "DATABASE_MAX_CONNECTIONS"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("%s is not reported: %v", fragment, err)
		}
	}
}

// s3Environment is a complete environment that selects the S3 backend, with
// the two secrets in files written the way `echo` writes them.
func s3Environment(t *testing.T) map[string]string {
	t.Helper()
	directory := t.TempDir()
	accessKeyFile := filepath.Join(directory, "access-key")
	secretKeyFile := filepath.Join(directory, "secret-key")
	if err := os.WriteFile(accessKeyFile, []byte("example-access\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretKeyFile, []byte("example-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := completeEnvironment()
	values["SOLOCLEF_API_FILE_STORAGE_BACKEND"] = "s3"
	values["SOLOCLEF_API_FILE_STORAGE_S3_ENDPOINT"] = "http://minio.example.internal:9000"
	values["SOLOCLEF_API_FILE_STORAGE_S3_BUCKET"] = "example-bucket"
	values["SOLOCLEF_API_FILE_STORAGE_S3_REGION"] = "us-east-1"
	values["SOLOCLEF_API_FILE_STORAGE_S3_PATH_STYLE"] = "true"
	values["SOLOCLEF_API_FILE_STORAGE_S3_ACCESS_KEY_FILE"] = accessKeyFile
	values["SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY_FILE"] = secretKeyFile
	return values
}

func TestLoadReadsTheBucketAndItsSecretsFromFiles(t *testing.T) {
	config, err := Load(fakeEnvironment(s3Environment(t)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.FileStorageS3Endpoint != "http://minio.example.internal:9000" ||
		config.FileStorageS3Bucket != "example-bucket" ||
		config.FileStorageS3Region != "us-east-1" ||
		!config.FileStorageS3PathStyle {
		t.Errorf("bucket settings: got %q %q %q path-style %v", config.FileStorageS3Endpoint,
			config.FileStorageS3Bucket, config.FileStorageS3Region, config.FileStorageS3PathStyle)
	}
	// The newline each file ends with is not part of the secret.
	if config.FileStorageS3AccessKey != "example-access" || config.FileStorageS3SecretKey != "example-secret" {
		t.Errorf("secrets: got %q and %q", config.FileStorageS3AccessKey, config.FileStorageS3SecretKey)
	}
}

// Naming s3 and leaving the bucket out must stop the process, with every
// missing setting named at once -- never a start that reads local disk.
func TestLoadReportsEveryBucketSettingWhenS3IsNamed(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_FILE_STORAGE_BACKEND"] = "s3"
	// A local path being present must not rescue it.
	values["SOLOCLEF_API_FILE_STORAGE_LOCAL_PATH"] = "/srv/objects"

	_, err := Load(fakeEnvironment(values))
	if err == nil {
		t.Fatal("want an error, got none")
	}
	for _, name := range []string{
		"SOLOCLEF_API_FILE_STORAGE_S3_ENDPOINT",
		"SOLOCLEF_API_FILE_STORAGE_S3_BUCKET",
		"SOLOCLEF_API_FILE_STORAGE_S3_REGION",
		"SOLOCLEF_API_FILE_STORAGE_S3_PATH_STYLE",
		"SOLOCLEF_API_FILE_STORAGE_S3_ACCESS_KEY_FILE",
		"SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY_FILE",
	} {
		if !strings.Contains(err.Error(), name+" is required") {
			t.Errorf("%s is not reported as required: %v", name, err)
		}
	}
}

// The local backend needs none of the bucket's settings: with none of them
// set, nothing of the bucket is read and no "s3" store will be opened.
func TestLoadReadsNoBucketForTheLocalBackendWhenNoneIsNamed(t *testing.T) {
	config, err := Load(fakeEnvironment(completeEnvironment()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.FileStorageLocalPath != "/srv/objects" {
		t.Errorf("local path: %q", config.FileStorageLocalPath)
	}
	if config.FileStorageS3Endpoint != "" || config.FileStorageS3Bucket != "" || config.FileStorageS3Region != "" ||
		config.FileStorageS3PathStyle || config.FileStorageS3AccessKey != "" || config.FileStorageS3SecretKey != "" {
		t.Errorf("bucket settings appeared from nowhere: %+v", config)
	}
}

// The backend that is written to must have its directory: local with no
// path stops the process, even with a whole bucket described beside it.
func TestLoadRequiresTheLocalPathForTheLocalBackend(t *testing.T) {
	values := s3Environment(t)
	values["SOLOCLEF_API_FILE_STORAGE_BACKEND"] = "local"
	delete(values, "SOLOCLEF_API_FILE_STORAGE_LOCAL_PATH")

	_, err := Load(fakeEnvironment(values))
	if err == nil || !strings.Contains(err.Error(),
		"SOLOCLEF_API_FILE_STORAGE_LOCAL_PATH is required when SOLOCLEF_API_FILE_STORAGE_BACKEND is local") {
		t.Errorf("got %v", err)
	}
}

// After a move to s3, the files written before it are rows recorded "local".
// Naming the directory beside the bucket is what keeps them readable; leaving
// it out is allowed, and then they are not.
func TestLoadTakesTheLocalPathBesideS3OnlyWhenItIsGiven(t *testing.T) {
	values := s3Environment(t)
	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("with the path: %v", err)
	}
	if config.FileStorageBackend != "s3" || config.FileStorageLocalPath != "/srv/objects" {
		t.Errorf("backend %q, local path %q", config.FileStorageBackend, config.FileStorageLocalPath)
	}

	delete(values, "SOLOCLEF_API_FILE_STORAGE_LOCAL_PATH")
	config, err = Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("without the path: %v", err)
	}
	if config.FileStorageLocalPath != "" {
		t.Errorf("a local path was invented: %q", config.FileStorageLocalPath)
	}
}

// The other direction: local is written to, and a whole bucket named beside
// it makes rows recorded "s3" readable. The backend stays what was named.
func TestLoadReadsAWholeBucketBesideTheLocalBackend(t *testing.T) {
	values := s3Environment(t)
	values["SOLOCLEF_API_FILE_STORAGE_BACKEND"] = "local"

	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.FileStorageBackend != "local" {
		t.Errorf("the bucket's settings changed the backend to %q", config.FileStorageBackend)
	}
	if config.FileStorageS3Bucket != "example-bucket" || config.FileStorageS3AccessKey != "example-access" ||
		config.FileStorageS3SecretKey != "example-secret" || !config.FileStorageS3PathStyle {
		t.Errorf("the bucket was not read: %q", config.FileStorageS3Bucket)
	}
}

// Half a bucket beside the local backend is a mistake, not a bucket to leave
// quietly unread: every missing setting is named, and so is the reason the
// bucket is being asked for.
func TestLoadRefusesHalfABucketBesideTheLocalBackend(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_FILE_STORAGE_S3_BUCKET"] = "example-bucket"
	values["SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY_FILE"] = "/nonexistent/secret-key"

	_, err := Load(fakeEnvironment(values))
	if err == nil {
		t.Fatal("want an error, got none")
	}
	for _, wanted := range []string{
		"SOLOCLEF_API_FILE_STORAGE_S3_ENDPOINT is required",
		"SOLOCLEF_API_FILE_STORAGE_S3_REGION is required",
		"SOLOCLEF_API_FILE_STORAGE_S3_PATH_STYLE is required",
		"SOLOCLEF_API_FILE_STORAGE_S3_ACCESS_KEY_FILE is required",
		`SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY_FILE names "/nonexistent/secret-key", which cannot be read`,
		`set in part, beside backend "local": set all six`,
	} {
		if !strings.Contains(err.Error(), wanted) {
			t.Errorf("the report does not say %q: %v", wanted, err)
		}
	}
	if strings.Contains(err.Error(), "FILE_STORAGE_S3_BUCKET is required") {
		t.Errorf("a setting that is present is reported missing: %v", err)
	}
}

func TestLoadRefusesALoosePathStyle(t *testing.T) {
	for _, value := range []string{"1", "yes", "TRUE", "path"} {
		values := s3Environment(t)
		values["SOLOCLEF_API_FILE_STORAGE_S3_PATH_STYLE"] = value
		_, err := Load(fakeEnvironment(values))
		if err == nil || !strings.Contains(err.Error(), "FILE_STORAGE_S3_PATH_STYLE must be true or false") {
			t.Errorf("%q: got %v, want it refused", value, err)
		}
	}

	values := s3Environment(t)
	values["SOLOCLEF_API_FILE_STORAGE_S3_PATH_STYLE"] = "false"
	config, err := Load(fakeEnvironment(values))
	if err != nil || config.FileStorageS3PathStyle {
		t.Errorf("false: got path-style %v and %v", config.FileStorageS3PathStyle, err)
	}
}

func TestLoadRefusesASecretFileItCannotUse(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct{ path, want string }{
		"a file that is not there":  {filepath.Join(t.TempDir(), "absent"), "which cannot be read"},
		"a file with nothing in it": {empty, "which is empty"},
	} {
		values := s3Environment(t)
		values["SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY_FILE"] = test.path
		_, err := Load(fakeEnvironment(values))
		if err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY_FILE") ||
			!strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// There is one way to give the credential. A plain-value variable beside
// the file one is not read, so a stale copy of the secret cannot be the one
// that is used.
func TestLoadTakesTheSecretOnlyFromItsFile(t *testing.T) {
	values := s3Environment(t)
	delete(values, "SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY_FILE")
	values["SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY"] = "pasted-inline"

	_, err := Load(fakeEnvironment(values))
	if err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_FILE_STORAGE_S3_SECRET_KEY_FILE is required") {
		t.Errorf("got %v, want the file variable reported as required", err)
	}
}

// A deployment that mounts its secrets gives the database password as a
// file. It is written into the URL percent-encoded, so a password made of
// the characters a URL gives meaning to still reaches Postgres as itself.
func TestLoadWritesTheDatabasePasswordFromItsFileIntoTheURL(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "postgres-password")
	if err := os.WriteFile(passwordFile, []byte("p@ss/w:rd?#\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := completeEnvironment()
	values["SOLOCLEF_API_DATABASE_URL"] = "postgres://notes@db.example.internal:5432/notes?sslmode=disable"
	values["SOLOCLEF_API_DATABASE_PASSWORD_FILE"] = passwordFile

	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	address, err := url.Parse(config.DatabaseURL)
	if err != nil {
		t.Fatalf("the URL no longer parses: %v", err)
	}
	password, _ := address.User.Password()
	if address.User.Username() != "notes" || password != "p@ss/w:rd?#" {
		t.Errorf("user %q, password %q", address.User.Username(), password)
	}
	if address.Host != "db.example.internal:5432" || address.Path != "/notes" || address.RawQuery != "sslmode=disable" {
		t.Errorf("the rest of the URL changed: %q", config.DatabaseURL)
	}
}

// With no file named, the URL is used exactly as given: nothing rewrites it.
func TestLoadLeavesTheDatabaseURLAloneWithoutAPasswordFile(t *testing.T) {
	config, err := Load(fakeEnvironment(completeEnvironment()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.DatabaseURL != "postgres://user:pw@db:5432/notes" {
		t.Errorf("got %q", config.DatabaseURL)
	}
}

func TestLoadRefusesADatabasePasswordItCannotPlace(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "postgres-password")
	if err := os.WriteFile(passwordFile, []byte("from-the-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct{ address, file, want string }{
		// Two passwords: one would be ignored, and silently.
		"a password in both places": {"postgres://notes:inline-secret@db:5432/notes", passwordFile, "give the password once"},
		// A URL with nobody to give the password to.
		"a URL with no user": {"postgres://db:5432/notes", passwordFile, "must be a URL that names a user"},
		"a file that is absent": {"postgres://notes@db:5432/notes", filepath.Join(t.TempDir(), "absent"),
			"SOLOCLEF_API_DATABASE_PASSWORD_FILE names"},
	} {
		values := completeEnvironment()
		values["SOLOCLEF_API_DATABASE_URL"] = test.address
		values["SOLOCLEF_API_DATABASE_PASSWORD_FILE"] = test.file
		_, err := Load(fakeEnvironment(values))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: got %v, want %q", name, err, test.want)
		}
		// Whatever is wrong, the report never repeats a password.
		if err != nil && (strings.Contains(err.Error(), "inline-secret") || strings.Contains(err.Error(), "from-the-file")) {
			t.Errorf("%s: the report repeats a password: %v", name, err)
		}
	}
}

// The session secret is given once: as a value or as a file, never both and
// never neither.
func TestLoadTakesTheSessionSecretAsAValueOrAFile(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "session-secret")
	if err := os.WriteFile(secretFile, []byte("secret-from-a-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	values := completeEnvironment()
	delete(values, "SOLOCLEF_API_SESSION_SECRET")
	values["SOLOCLEF_API_SESSION_SECRET_FILE"] = secretFile
	config, err := Load(fakeEnvironment(values))
	if err != nil || config.SessionSecret != "secret-from-a-file" {
		t.Errorf("from a file: got %q and %v", config.SessionSecret, err)
	}

	config, err = Load(fakeEnvironment(completeEnvironment()))
	if err != nil || config.SessionSecret != "test-secret" {
		t.Errorf("as a value: got %q and %v", config.SessionSecret, err)
	}

	values = completeEnvironment()
	values["SOLOCLEF_API_SESSION_SECRET_FILE"] = secretFile
	if _, err := Load(fakeEnvironment(values)); err == nil ||
		!strings.Contains(err.Error(), "SOLOCLEF_API_SESSION_SECRET and SOLOCLEF_API_SESSION_SECRET_FILE are both set") {
		t.Errorf("both: got %v", err)
	}

	values = completeEnvironment()
	delete(values, "SOLOCLEF_API_SESSION_SECRET")
	if _, err := Load(fakeEnvironment(values)); err == nil ||
		!strings.Contains(err.Error(), "SOLOCLEF_API_SESSION_SECRET is required") {
		t.Errorf("neither: got %v", err)
	}

	// A named file that cannot be used is not rescued by anything.
	values = completeEnvironment()
	delete(values, "SOLOCLEF_API_SESSION_SECRET")
	values["SOLOCLEF_API_SESSION_SECRET_FILE"] = filepath.Join(t.TempDir(), "absent")
	if _, err := Load(fakeEnvironment(values)); err == nil ||
		!strings.Contains(err.Error(), "SOLOCLEF_API_SESSION_SECRET_FILE names") {
		t.Errorf("an absent file: got %v", err)
	}
}

// The speech key may be left out (a proxy that asks for none), given as a
// value, or given as a file, and never both ways at once.
func TestLoadTakesTheSpeechKeyAsNothingAValueOrAFile(t *testing.T) {
	config, err := Load(fakeEnvironment(completeEnvironment()))
	if err != nil || config.TTSServiceAPIKey != "" {
		t.Errorf("neither: got %q and %v", config.TTSServiceAPIKey, err)
	}

	values := completeEnvironment()
	values["SOLOCLEF_API_TTS_SERVICE_API_KEY"] = "sk-speech"
	if config, err = Load(fakeEnvironment(values)); err != nil || config.TTSServiceAPIKey != "sk-speech" {
		t.Errorf("as a value: got %q and %v", config.TTSServiceAPIKey, err)
	}

	keyFile := filepath.Join(t.TempDir(), "speech-key")
	if err := os.WriteFile(keyFile, []byte("sk-speech-from-a-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values = completeEnvironment()
	values["SOLOCLEF_API_TTS_SERVICE_API_KEY_FILE"] = keyFile
	if config, err = Load(fakeEnvironment(values)); err != nil || config.TTSServiceAPIKey != "sk-speech-from-a-file" {
		t.Errorf("from a file: got %q and %v", config.TTSServiceAPIKey, err)
	}

	values["SOLOCLEF_API_TTS_SERVICE_API_KEY"] = "sk-speech"
	if _, err := Load(fakeEnvironment(values)); err == nil ||
		!strings.Contains(err.Error(), "SOLOCLEF_API_TTS_SERVICE_API_KEY and SOLOCLEF_API_TTS_SERVICE_API_KEY_FILE are both set") {
		t.Errorf("both: got %v", err)
	}

	values = completeEnvironment()
	values["SOLOCLEF_API_TTS_SERVICE_API_KEY_FILE"] = filepath.Join(t.TempDir(), "absent")
	if _, err := Load(fakeEnvironment(values)); err == nil ||
		!strings.Contains(err.Error(), "SOLOCLEF_API_TTS_SERVICE_API_KEY_FILE names") {
		t.Errorf("an absent file: got %v", err)
	}
}

// Who may make an account, whether single sign-on is on and whether mail is
// sent are each said outright: a deployment that leaves one out is told, and
// "yes" or "1" is not a way of saying true.
func TestLoadRequiresTheThreeSignInSwitchesAndReadsThemStrictly(t *testing.T) {
	for _, name := range []string{"SIGNUP_ALLOWED", "OIDC_ENABLED", "SMTP_ENABLED"} {
		values := completeEnvironment()
		delete(values, "SOLOCLEF_API_"+name)
		if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_"+name+" is required") {
			t.Errorf("without %s: %v", name, err)
		}
		values["SOLOCLEF_API_"+name] = "yes"
		if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_"+name+" must be true or false") {
			t.Errorf("%s=yes: %v", name, err)
		}
	}
	values := completeEnvironment()
	values["SOLOCLEF_API_SIGNUP_ALLOWED"] = "true"
	settings, err := Load(fakeEnvironment(values))
	if err != nil || !settings.SignupAllowed || settings.OIDCEnabled || settings.SMTPEnabled {
		t.Errorf("read %+v, %v", settings.SignupAllowed, err)
	}
}

// With single sign-on off, none of its settings is read: a secret file that
// does not exist is not a problem. With it on, every one is required, the
// secret comes from its file, and the web app's address is required with it.
func TestLoadReadsSingleSignOnWholeOrNotAtAll(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_OIDC_CLIENT_SECRET_FILE"] = "/nonexistent/secret"
	if _, err := Load(fakeEnvironment(values)); err != nil {
		t.Fatalf("single sign-on off, a stale secret path: %v", err)
	}

	values = completeEnvironment()
	values["SOLOCLEF_API_OIDC_ENABLED"] = "true"
	_, err := Load(fakeEnvironment(values))
	for _, name := range []string{"OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET_FILE", "OIDC_REDIRECT_URL", "OIDC_DISPLAY_NAME", "FRONTEND_URL"} {
		if err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_"+name+" is required") {
			t.Errorf("single sign-on on, without %s: %v", name, err)
		}
	}

	secret := filepath.Join(t.TempDir(), "oidc-secret")
	if err := os.WriteFile(secret, []byte("example-client-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values["SOLOCLEF_API_OIDC_ISSUER"] = "https://sso.example.com/application/o/notes/"
	values["SOLOCLEF_API_OIDC_CLIENT_ID"] = "notes-client"
	values["SOLOCLEF_API_OIDC_CLIENT_SECRET_FILE"] = secret
	values["SOLOCLEF_API_OIDC_REDIRECT_URL"] = "https://notes.example.com/auth/oidc/callback"
	values["SOLOCLEF_API_FRONTEND_URL"] = "https://notes.example.com"
	values["SOLOCLEF_API_OIDC_DISPLAY_NAME"] = " Example   SSO "
	values["SOLOCLEF_API_OIDC_APP_REDIRECTS"] = "soloclef://auth, soloclef-dev://auth"
	settings, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatal(err)
	}
	// The button's words are optional (the website's own when empty); the
	// name is said and tidied; the app's addresses are a list.
	if settings.OIDCButtonLabel != "" || settings.OIDCDisplayName != "Example SSO" ||
		strings.Join(settings.OIDCAppRedirects, "|") != "soloclef://auth|soloclef-dev://auth" {
		t.Errorf("read label %q, name %q, app addresses %q", settings.OIDCButtonLabel, settings.OIDCDisplayName, settings.OIDCAppRedirects)
	}
	for _, wrong := range []string{"soloclef://auth?code=x", "auth", "soloclef://auth#x"} {
		values["SOLOCLEF_API_OIDC_APP_REDIRECTS"] = wrong
		if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), "OIDC_APP_REDIRECTS must list") {
			t.Errorf("an app return address %q: %v", wrong, err)
		}
	}
	values["SOLOCLEF_API_OIDC_APP_REDIRECTS"] = ""
	settings, err = Load(fakeEnvironment(values))
	if err != nil || len(settings.OIDCAppRedirects) != 0 {
		t.Errorf("no app addresses is valid and means none: %v %q", err, settings.OIDCAppRedirects)
	}
	if settings.OIDCClientSecret != "example-client-secret" || settings.OIDCIssuer != "https://sso.example.com/application/o/notes/" ||
		settings.OIDCClientID != "notes-client" || settings.OIDCRedirectURL != "https://notes.example.com/auth/oidc/callback" {
		t.Errorf("read %q %q %q", settings.OIDCIssuer, settings.OIDCClientID, settings.OIDCRedirectURL)
	}

	values["SOLOCLEF_API_OIDC_REDIRECT_URL"] = "notes.example.com/auth/oidc/callback"
	if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_OIDC_REDIRECT_URL must be an http(s) address") {
		t.Errorf("a redirect address with no scheme: %v", err)
	}
}

// Mail is the same shape: unread when off, whole when on, with no guessed
// security mode and a password exactly when there is a username.
func TestLoadReadsMailWholeOrNotAtAll(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_SMTP_ENABLED"] = "true"
	_, err := Load(fakeEnvironment(values))
	for _, name := range []string{"SMTP_HOST", "SMTP_PORT", "SMTP_FROM", "SMTP_TIMEOUT_SECONDS", "SMTP_SECURITY", "FRONTEND_URL"} {
		if err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_"+name+" is required") {
			t.Errorf("mail on, without %s: %v", name, err)
		}
	}

	values["SOLOCLEF_API_SMTP_HOST"] = "mail.example.com"
	values["SOLOCLEF_API_SMTP_PORT"] = "587"
	values["SOLOCLEF_API_SMTP_FROM"] = "Notes <notes@example.com>"
	values["SOLOCLEF_API_SMTP_TIMEOUT_SECONDS"] = "20"
	values["SOLOCLEF_API_SMTP_SECURITY"] = "starttls"
	values["SOLOCLEF_API_FRONTEND_URL"] = "https://notes.example.com"
	settings, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatal(err)
	}
	if settings.SMTPHost != "mail.example.com" || settings.SMTPPort != 587 || settings.SMTPSecurity != "starttls" ||
		settings.SMTPTimeout != 20*time.Second || settings.SMTPUsername != "" || settings.SMTPPassword != "" {
		t.Errorf("read %+v", settings.SMTPHost)
	}

	values["SOLOCLEF_API_SMTP_USERNAME"] = "notes"
	if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), "set together or not at all") {
		t.Errorf("a username with no password file: %v", err)
	}
	values["SOLOCLEF_API_SMTP_SECURITY"] = "ssl"
	if _, err := Load(fakeEnvironment(values)); err == nil || !strings.Contains(err.Error(), "SMTP_SECURITY must be starttls, tls or none") {
		t.Errorf("an unknown security mode: %v", err)
	}
}

// The end-to-end routes hand out sessions, so a stack says whether it has
// them, names its own secret when it does, and cannot also send mail.
func TestLoadReadsTheEndToEndRoutesWholeOrNotAtAll(t *testing.T) {
	environment := completeEnvironment()
	delete(environment, "SOLOCLEF_API_E2E_ROUTES")
	if _, err := Load(func(name string) string { return environment[name] }); err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_E2E_ROUTES is required") {
		t.Errorf("no switch: %v", err)
	}

	// Off: the secret is not read, even when one is lying about.
	environment["SOLOCLEF_API_E2E_ROUTES"] = "false"
	environment["SOLOCLEF_API_E2E_SEED_SECRET"] = "left-over-from-a-test-stack"
	loaded, err := Load(func(name string) string { return environment[name] })
	if err != nil || loaded.E2ERoutes || loaded.E2ESeedSecret != "" {
		t.Errorf("off: routes %v, secret %q, %v", loaded.E2ERoutes, loaded.E2ESeedSecret, err)
	}

	// On: the secret is required, and mail must be off.
	environment["SOLOCLEF_API_E2E_ROUTES"] = "true"
	delete(environment, "SOLOCLEF_API_E2E_SEED_SECRET")
	if _, err := Load(func(name string) string { return environment[name] }); err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_E2E_SEED_SECRET is required") {
		t.Errorf("on with no secret: %v", err)
	}
	environment["SOLOCLEF_API_E2E_SEED_SECRET"] = "a-secret-of-this-stack-only"
	loaded, err = Load(func(name string) string { return environment[name] })
	if err != nil || !loaded.E2ERoutes || loaded.E2ESeedSecret != "a-secret-of-this-stack-only" {
		t.Errorf("on: routes %v, secret %q, %v", loaded.E2ERoutes, loaded.E2ESeedSecret, err)
	}
	environment["SOLOCLEF_API_SMTP_ENABLED"] = "true"
	if _, err := Load(func(name string) string { return environment[name] }); err == nil || !strings.Contains(err.Error(), "SOLOCLEF_API_SMTP_ENABLED must be false when SOLOCLEF_API_E2E_ROUTES is true") {
		t.Errorf("on with mail: %v", err)
	}
}

func TestLoadReadsTheSupportAddressOrNone(t *testing.T) {
	environment := completeEnvironment()
	loaded, err := Load(func(name string) string { return environment[name] })
	if err != nil || loaded.SupportEmail != "" {
		t.Errorf("unset: %q, %v", loaded.SupportEmail, err)
	}
	environment["SOLOCLEF_API_SUPPORT_EMAIL"] = " help@notes.example.test "
	loaded, err = Load(func(name string) string { return environment[name] })
	if err != nil || loaded.SupportEmail != "help@notes.example.test" {
		t.Errorf("set: %q, %v", loaded.SupportEmail, err)
	}
}
