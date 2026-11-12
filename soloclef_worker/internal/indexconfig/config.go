// Package indexconfig reads the indexing worker's settings.
//
// Every setting is named, and every one but the Qdrant key and the vision
// model is required. None has a default: an address, a bucket or a retry count written here would be
// one deployment's value shipped to every other, and a worker that started
// on a guess would index into the wrong place without saying so. Everything
// that is missing or wrong is reported at once, so a deployment missing five
// settings is told about five.
package indexconfig

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// The settings. The parsers' own (SOLOCLEF_PARSER_DEFAULT and the rest) are
// read by soloclef_ingest/parserconfig, and the queue's by internal/broker.
const (
	// DatabaseURL is the Postgres the documents and chunks are in.
	DatabaseURL = "SOLOCLEF_INDEX_DATABASE_URL"

	// EmbeddingProfile names the embedding choice (internal/embedding).
	EmbeddingProfile = "SOLOCLEF_EMBEDDING_PROFILE"
	// TokenizerDirectory holds one folder per profile with its
	// tokenizer.json. Required when the profile counts tokens.
	TokenizerDirectory = "SOLOCLEF_INDEX_TOKENIZER_DIR"

	// ProxyURL is the model proxy's OpenAI-compatible root, ending in /v1,
	// and ProxyKeyFile the file its key is in. They say where the proxy is,
	// not which model is used.
	ProxyURL     = "SOLOCLEF_INDEX_LITELLM_URL"
	ProxyKeyFile = "SOLOCLEF_INDEX_LITELLM_KEY_FILE"
	// EmbeddingTimeout bounds one embedding request.
	EmbeddingTimeout = "SOLOCLEF_INDEX_EMBEDDING_TIMEOUT"
	// VisionModel is the proxy's name for the model that describes a
	// document's pictures when its upload asked for vision and its owner
	// chose no vision model of their own: the API's CHAT_VISION_MODEL, the
	// same default the upload route checked. Optional: with none, only an
	// account that chose a model can ask for vision, and the API refuses
	// the others before anything is stored.
	VisionModel = "SOLOCLEF_INDEX_VISION_MODEL"
	// SecretKeyFile is the file holding the deployment's secret key, the
	// API's and the worker's (SOLOCLEF_WORKER_SECRET_KEY_FILE): an owner
	// whose vision model is one of their own model connections has its key
	// opened with it (soloclef_assistant/ownmodels). Required, because any
	// owner may choose one, and a document of theirs must not fail for a
	// setting the deployment left out.
	SecretKeyFile = "SOLOCLEF_INDEX_SECRET_KEY_FILE"

	// QdrantURL is Qdrant's HTTP address. QdrantAlias is the name searches
	// address; QdrantCollection is what it points at and what is created
	// when it does not exist.
	QdrantURL        = "SOLOCLEF_INDEX_QDRANT_URL"
	QdrantAlias      = "SOLOCLEF_INDEX_QDRANT_ALIAS"
	QdrantCollection = "SOLOCLEF_INDEX_QDRANT_COLLECTION"
	// QdrantKeyFile is the one optional setting: Qdrant inside the stack's
	// own network runs without a key, and then there is no file to name.
	QdrantKeyFile = "SOLOCLEF_INDEX_QDRANT_API_KEY_FILE"
	// QdrantTimeout bounds one Qdrant request.
	QdrantTimeout = "SOLOCLEF_INDEX_QDRANT_TIMEOUT"

	// DefaultQueue is the deployment's default queue, the worker's
	// SOLOCLEF_WORKER_DEFAULT_QUEUE: a document made ready asks there for
	// the notebooks reading it to be named (internal/namerequest).
	DefaultQueue = "SOLOCLEF_INDEX_DEFAULT_QUEUE"

	// MaxAttempts and RetryDelay bound how a failure that may pass is
	// retried (indexing.Config).
	MaxAttempts = "SOLOCLEF_INDEX_MAX_ATTEMPTS"
	RetryDelay  = "SOLOCLEF_INDEX_RETRY_DELAY"

	// The file store, by the names the API tier uses under its own prefix.
	FileStorageBackend     = "SOLOCLEF_INDEX_FILE_STORAGE_BACKEND"
	FileStorageLocalPath   = "SOLOCLEF_INDEX_FILE_STORAGE_LOCAL_PATH"
	FileStorageS3Endpoint  = "SOLOCLEF_INDEX_FILE_STORAGE_S3_ENDPOINT"
	FileStorageS3Bucket    = "SOLOCLEF_INDEX_FILE_STORAGE_S3_BUCKET"
	FileStorageS3Region    = "SOLOCLEF_INDEX_FILE_STORAGE_S3_REGION"
	FileStorageS3PathStyle = "SOLOCLEF_INDEX_FILE_STORAGE_S3_PATH_STYLE"
	FileStorageS3AccessKey = "SOLOCLEF_INDEX_FILE_STORAGE_S3_ACCESS_KEY_FILE"
	FileStorageS3SecretKey = "SOLOCLEF_INDEX_FILE_STORAGE_S3_SECRET_KEY_FILE"
)

// Config is the settings, read and checked.
type Config struct {
	DatabaseURL string

	EmbeddingProfile   string
	TokenizerDirectory string
	ProxyURL           string
	ProxyKey           string
	EmbeddingTimeout   time.Duration
	VisionModel        string
	SecretKey          string

	QdrantURL        string
	QdrantAlias      string
	QdrantCollection string
	QdrantKey        string
	QdrantTimeout    time.Duration

	DefaultQueue string

	MaxAttempts int
	RetryDelay  time.Duration

	FileStorage filestore.Settings
}

// Load reads the settings from an environment, or says everything that is
// wrong with it.
func Load(getenv func(string) string) (Config, error) {
	var problems []string
	value := func(name string) string { return strings.TrimSpace(getenv(name)) }
	required := func(name string) string {
		found := value(name)
		if found == "" {
			problems = append(problems, name+" is not set")
		}
		return found
	}
	duration := func(name string) time.Duration {
		raw := required(name)
		if raw == "" {
			return 0
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			problems = append(problems, fmt.Sprintf("%s=%q is not a duration greater than zero (for example 30s)", name, raw))
		}
		return parsed
	}
	// A secret is named by the file it is in, so it is never in the
	// environment of the process or in the output of `docker inspect`.
	secret := func(name string) string {
		path := value(name)
		if path == "" {
			return ""
		}
		content, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s names %q, which cannot be read: %v", name, path, err))
			return ""
		}
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			problems = append(problems, fmt.Sprintf("%s names %q, which is empty", name, path))
		}
		return trimmed
	}
	requiredSecret := func(name string) string {
		if required(name) == "" {
			return ""
		}
		return secret(name)
	}

	config := Config{
		DatabaseURL:        required(DatabaseURL),
		EmbeddingProfile:   required(EmbeddingProfile),
		TokenizerDirectory: value(TokenizerDirectory),
		ProxyURL:           required(ProxyURL),
		ProxyKey:           requiredSecret(ProxyKeyFile),
		EmbeddingTimeout:   duration(EmbeddingTimeout),
		VisionModel:        value(VisionModel),
		SecretKey:          requiredSecret(SecretKeyFile),
		QdrantURL:          required(QdrantURL),
		QdrantAlias:        required(QdrantAlias),
		QdrantCollection:   required(QdrantCollection),
		QdrantKey:          secret(QdrantKeyFile),
		QdrantTimeout:      duration(QdrantTimeout),
		DefaultQueue:       required(DefaultQueue),
		RetryDelay:         duration(RetryDelay),
	}

	if raw := required(MaxAttempts); raw != "" {
		attempts, err := strconv.Atoi(raw)
		if err != nil || attempts < 1 {
			problems = append(problems, fmt.Sprintf("%s=%q is not a whole number of at least 1", MaxAttempts, raw))
		}
		config.MaxAttempts = attempts
	}

	// The file store: the configured backend's settings are required, and
	// the other backend's are read when they are given, so that rows written
	// before a move from one to the other can still be read (see
	// filestore.Stores). Which settings are missing for the backend that was
	// named is the store's to say; it knows.
	config.FileStorage = filestore.Settings{
		Backend:   required(FileStorageBackend),
		LocalRoot: value(FileStorageLocalPath),
		S3: filestore.S3Settings{
			Endpoint:  value(FileStorageS3Endpoint),
			Bucket:    value(FileStorageS3Bucket),
			Region:    value(FileStorageS3Region),
			AccessKey: secret(FileStorageS3AccessKey),
			SecretKey: secret(FileStorageS3SecretKey),
		},
	}
	switch style := value(FileStorageS3PathStyle); style {
	case "true":
		config.FileStorage.S3.PathStyle = true
	case "false":
	case "":
		// Only asked for when a bucket is described at all.
		if config.FileStorage.S3 != (filestore.S3Settings{}) || config.FileStorage.Backend == filestore.S3Backend {
			problems = append(problems, FileStorageS3PathStyle+" is not set (true for MinIO, false for a store that addresses a bucket by host name)")
		}
	default:
		problems = append(problems, fmt.Sprintf("%s=%q must be true or false", FileStorageS3PathStyle, style))
	}

	if len(problems) > 0 {
		return Config{}, errors.New("the indexing worker's settings are incomplete:\n  " + strings.Join(problems, "\n  "))
	}
	return config, nil
}
