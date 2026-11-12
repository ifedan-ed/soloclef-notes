package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// storageEnvironment is the least a process needs to load its settings,
// with the file store's part left to each test.
func storageEnvironment(t *testing.T, storage map[string]string) func(string) string {
	t.Helper()
	// The model proxy's key is required and is read from a file.
	proxyKeyFile := filepath.Join(t.TempDir(), "litellm-key")
	if err := os.WriteFile(proxyKeyFile, []byte("sk-example-proxy-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
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
		"SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES":      "524288000",
		"SOLOCLEF_API_UPLOAD_MAX_FILES":           "10",
		"SOLOCLEF_API_NOTE_INDEX_DELAY_SECONDS":   "15",
		"SOLOCLEF_API_SANDBOX_ENABLED":            "false",
		"SOLOCLEF_API_SIGNUP_ALLOWED":             "false",
		"SOLOCLEF_API_OIDC_ENABLED":               "false",
		"SOLOCLEF_API_SMTP_ENABLED":               "false",
		"SOLOCLEF_API_E2E_ROUTES":                 "false",
		"SOLOCLEF_API_LISTEN_ADDRESS":             "127.0.0.1:8081",
		"SOLOCLEF_API_DATABASE_URL":               "postgres://user:pw@db:5432/notes",
		"SOLOCLEF_API_SESSION_SECRET":             "test-secret",
		"SOLOCLEF_API_ERROR_REPORT_URL":           "https://notes.example.com/contact",
		"SOLOCLEF_API_REDIS_URL":                  "redis://redis:6379/0",
		"SOLOCLEF_API_CELERY_BROKER_URL":          "redis://redis:6379/0",
		"SOLOCLEF_API_CELERY_DEFAULT_QUEUE":       "surfsense",
	}
	for name, value := range storage {
		values["SOLOCLEF_API_FILE_STORAGE_"+name] = value
	}
	return func(name string) string { return values[name] }
}

// The defect this file's function fixed: the bucket's settings were loaded
// and then dropped on the way to the file store, so naming s3 could never
// start. Every one of them, read from the environment as a deployment gives
// them, has to arrive -- and in its own field: the values are all different,
// so two swapped would show.
func TestEveryFileStorageSettingReachesTheFileStore(t *testing.T) {
	directory := t.TempDir()
	accessKeyFile := filepath.Join(directory, "access-key")
	secretKeyFile := filepath.Join(directory, "secret-key")
	for path, content := range map[string]string{accessKeyFile: "example-access\n", secretKeyFile: "example-secret\n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := config.Load(storageEnvironment(t, map[string]string{
		"BACKEND":            "s3",
		"LOCAL_PATH":         "/srv/objects",
		"S3_ENDPOINT":        "http://minio.example.internal:9000",
		"S3_BUCKET":          "example-bucket",
		"S3_REGION":          "example-region-1",
		"S3_PATH_STYLE":      "true",
		"S3_ACCESS_KEY_FILE": accessKeyFile,
		"S3_SECRET_KEY_FILE": secretKeyFile,
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := fileStorageSettings(loaded)
	want := filestore.Settings{
		Backend:   "s3",
		LocalRoot: "/srv/objects",
		S3: filestore.S3Settings{
			Endpoint:  "http://minio.example.internal:9000",
			Bucket:    "example-bucket",
			Region:    "example-region-1",
			PathStyle: true,
			AccessKey: "example-access",
			SecretKey: "example-secret",
		},
	}
	if got != want {
		t.Errorf("the file store was given\n%+v\nwant\n%+v", got, want)
	}
}

// From the environment to an open file, for the backend that needs no
// server: the settings a local deployment gives open a set that reads its
// rows, holds no bucket, and refuses a row that names one.
func TestALocalDeploymentsSettingsOpenAStoreThatReadsItsRows(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "greeting.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(storageEnvironment(t, map[string]string{"BACKEND": "local", "LOCAL_PATH": directory}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	stores, err := filestore.OpenStores(fileStorageSettings(loaded))
	if err != nil {
		t.Fatalf("OpenStores: %v", err)
	}
	defer stores.Close()

	local, err := stores.Named("local")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := local.Open(context.Background(), "greeting.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if content, err := io.ReadAll(stream); err != nil || string(content) != "hello" {
		t.Errorf("read %q, %v", content, err)
	}
	if _, err := stores.Named("s3"); !errors.Is(err, filestore.ErrBackendNotRead) {
		t.Errorf("a bucket nobody configured answered %v", err)
	}
}
