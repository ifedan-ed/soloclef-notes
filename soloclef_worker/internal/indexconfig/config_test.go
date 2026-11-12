package indexconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// complete is a full set of settings for a MinIO deployment, with its two
// secrets and the proxy key in files, as they are deployed.
func complete(t *testing.T) map[string]string {
	t.Helper()
	directory := t.TempDir()
	file := func(name, content string) string {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return map[string]string{
		DatabaseURL:            "postgres://index@db/soloclef",
		EmbeddingProfile:       "bge-m3",
		TokenizerDirectory:     "/opt/tokenizers",
		ProxyURL:               "http://proxy:4000/v1",
		ProxyKeyFile:           file("proxy", "sk-proxy\n"),
		EmbeddingTimeout:       "90s",
		SecretKeyFile:          file("secret-key", "fake-deployment-secret\n"),
		QdrantURL:              "http://qdrant:6333",
		QdrantAlias:            "chunks",
		QdrantCollection:       "chunks_bge-m3_1",
		QdrantTimeout:          "30s",
		DefaultQueue:           "notes",
		MaxAttempts:            "5",
		RetryDelay:             "20s",
		FileStorageBackend:     "s3",
		FileStorageS3Endpoint:  "http://minio:9000",
		FileStorageS3Bucket:    "soloclef",
		FileStorageS3Region:    "us-east-1",
		FileStorageS3PathStyle: "true",
		FileStorageS3AccessKey: file("access", "access\n"),
		FileStorageS3SecretKey: file("secret", "secret\n"),
	}
}

func load(settings map[string]string) (Config, error) {
	return Load(func(name string) string { return settings[name] })
}

func TestACompleteEnvironmentIsRead(t *testing.T) {
	config, err := load(complete(t))
	if err != nil {
		t.Fatal(err)
	}
	if config.ProxyKey != "sk-proxy" || config.SecretKey != "fake-deployment-secret" || config.EmbeddingTimeout != 90*time.Second || config.MaxAttempts != 5 || config.RetryDelay != 20*time.Second ||
		config.DefaultQueue != "notes" {
		t.Errorf("config = %+v", config)
	}
	storage := config.FileStorage
	if storage.Backend != "s3" || !storage.S3.PathStyle || storage.S3.AccessKey != "access" || storage.S3.SecretKey != "secret" || storage.S3.Bucket != "soloclef" {
		t.Errorf("file storage = %+v", storage)
	}
	// Qdrant with no key is a setting left out, not a setting guessed.
	if config.QdrantKey != "" {
		t.Errorf("a Qdrant key appeared from nowhere: %q", config.QdrantKey)
	}
	// So is a vision model: none set is none, not a name made up here.
	if config.VisionModel != "" {
		t.Errorf("a vision model appeared from nowhere: %q", config.VisionModel)
	}
	settings := complete(t)
	settings[VisionModel] = " vision-model "
	if config, err = load(settings); err != nil || config.VisionModel != "vision-model" {
		t.Errorf("the vision model = %q, %v", config.VisionModel, err)
	}
}

func TestEverySettingButTheQdrantKeyIsRequiredAndNoneIsGuessed(t *testing.T) {
	for _, name := range []string{
		DatabaseURL, EmbeddingProfile, ProxyURL, ProxyKeyFile, EmbeddingTimeout, SecretKeyFile,
		QdrantURL, QdrantAlias, QdrantCollection, QdrantTimeout, DefaultQueue, MaxAttempts, RetryDelay,
		FileStorageBackend, FileStorageS3PathStyle,
	} {
		settings := complete(t)
		delete(settings, name)
		_, err := load(settings)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("without %s: err = %v", name, err)
		}
	}
}

func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	_, err := load(map[string]string{MaxAttempts: "0", RetryDelay: "soon"})
	if err == nil {
		t.Fatal("an empty environment was accepted")
	}
	for _, name := range []string{DatabaseURL, EmbeddingProfile, ProxyURL, QdrantURL, MaxAttempts, RetryDelay, FileStorageBackend} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not mention %s:\n%v", name, err)
		}
	}
}

func TestValuesThatCannotWorkAreRefused(t *testing.T) {
	for name, change := range map[string]func(map[string]string){
		"no attempts":           func(s map[string]string) { s[MaxAttempts] = "0" },
		"attempts not a number": func(s map[string]string) { s[MaxAttempts] = "many" },
		"a delay with no unit":  func(s map[string]string) { s[RetryDelay] = "20" },
		"a timeout of nothing":  func(s map[string]string) { s[QdrantTimeout] = "0s" },
		"a key file not there":  func(s map[string]string) { s[ProxyKeyFile] = "/nonexistent/key" },
		"path style not a bool": func(s map[string]string) { s[FileStorageS3PathStyle] = "yes" },
	} {
		settings := complete(t)
		change(settings)
		if _, err := load(settings); err == nil {
			t.Errorf("%s: the settings were accepted", name)
		}
	}
	settings := complete(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings[ProxyKeyFile] = empty
	if _, err := load(settings); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty key file: err = %v", err)
	}
}

func TestALocalStoreNeedsNoBucketSettings(t *testing.T) {
	settings := complete(t)
	for _, name := range []string{FileStorageS3Endpoint, FileStorageS3Bucket, FileStorageS3Region, FileStorageS3PathStyle, FileStorageS3AccessKey, FileStorageS3SecretKey} {
		delete(settings, name)
	}
	settings[FileStorageBackend] = "local"
	settings[FileStorageLocalPath] = "/data/files"
	config, err := load(settings)
	if err != nil {
		t.Fatal(err)
	}
	if config.FileStorage.Backend != "local" || config.FileStorage.LocalRoot != "/data/files" {
		t.Errorf("file storage = %+v", config.FileStorage)
	}
}
