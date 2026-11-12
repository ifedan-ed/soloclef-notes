// Package workerconfig reads the settings of the jobs the worker runs
// (cmd/worker): the model proxy and the two models, the speech service, the
// vector store and the file store the jobs write to, and where a podcast
// is made.
//
// Every setting is named and every one is required, except the two a
// deployment may truly leave empty (the speaking style, the Qdrant key).
// None has a default: an address, a model or a limit written here would be
// one deployment's value shipped to every other. Everything missing or
// wrong is reported at once. The queue's settings are internal/broker's
// and the database's is cmd/worker's own.
package workerconfig

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// The settings.
const (
	// ProxyURL is the model proxy's OpenAI-compatible root, ending in /v1;
	// ProxyKeyFile the file its key is in. Chat and speech both go there.
	ProxyURL     = "SOLOCLEF_WORKER_LITELLM_URL"
	ProxyKeyFile = "SOLOCLEF_WORKER_LITELLM_KEY_FILE"
	// ChatModel is the proxy's name for the deployment's chat model, the
	// API tier's CHAT_MODEL: a podcast is drafted by the account's own
	// chat model, or by this one when it chose none.
	ChatModel = "SOLOCLEF_WORKER_CHAT_MODEL"
	// QuickModel is the model short calls are made with: a notebook's name.
	QuickModel = "SOLOCLEF_WORKER_QUICK_MODEL"
	// SecretKeyFile is the file holding the deployment's secret key, the
	// API's SOLOCLEF_API_SESSION_SECRET: what the API sealed the key and the
	// headers of an account's own model connection with, and so what a
	// podcast or a video drafted by an account's own model opens them with
	// (soloclef_assistant/ownmodels). The automation and Drive jobs read
	// the same file under the same name.
	SecretKeyFile = "SOLOCLEF_WORKER_SECRET_KEY_FILE"
	// CallTimeout bounds one model call, its retries included.
	CallTimeout = "SOLOCLEF_WORKER_LLM_CALL_TIMEOUT"

	// SpeechService is `<roster>/<model>`, the same value as the API tier's
	// SOLOCLEF_API_TTS_SERVICE: the API accepts a brief's voices against
	// that roster, and this worker speaks them.
	SpeechService = "SOLOCLEF_WORKER_TTS_SERVICE"
	// SpeechInstructions is the speaking style for a model that takes one.
	// May be empty: no style.
	SpeechInstructions = "SOLOCLEF_WORKER_TTS_INSTRUCTIONS"
	// SpeechAttemptTimeout, SpeechMaxAttempts and SpeechConcurrency are how
	// long one line may take, how many requests it may cost, and how many
	// lines one podcast voices at once.
	SpeechAttemptTimeout = "SOLOCLEF_WORKER_TTS_ATTEMPT_TIMEOUT"
	SpeechMaxAttempts    = "SOLOCLEF_WORKER_TTS_MAX_ATTEMPTS"
	SpeechConcurrency    = "SOLOCLEF_WORKER_TTS_CONCURRENCY"
	// PodcastWorkDirectory holds a podcast's voiced lines while it is made.
	PodcastWorkDirectory = "SOLOCLEF_WORKER_PODCAST_WORKDIR"

	// DefaultQueue is the deployment's default queue, the API tier's
	// CELERY_TASK_DEFAULT_QUEUE: what this worker publishes itself (the
	// reconciler's tick, and the jobs it republishes) is routed from it
	// exactly as the API routes (soloclef_celery/routing), so neither tier
	// can put work where the other is not looking.
	DefaultQueue = "SOLOCLEF_WORKER_DEFAULT_QUEUE"

	// The vector store a removal deletes points from: the alias searches
	// address, which the indexer keeps pointed at the live collection.
	QdrantURL     = "SOLOCLEF_WORKER_QDRANT_URL"
	QdrantAlias   = "SOLOCLEF_WORKER_QDRANT_ALIAS"
	QdrantKeyFile = "SOLOCLEF_WORKER_QDRANT_API_KEY_FILE"
	QdrantTimeout = "SOLOCLEF_WORKER_QDRANT_TIMEOUT"

	// The file store, by the names the indexer and the API use under their
	// own prefixes.
	FileStorageBackend     = "SOLOCLEF_WORKER_FILE_STORAGE_BACKEND"
	FileStorageLocalPath   = "SOLOCLEF_WORKER_FILE_STORAGE_LOCAL_PATH"
	FileStorageS3Endpoint  = "SOLOCLEF_WORKER_FILE_STORAGE_S3_ENDPOINT"
	FileStorageS3Bucket    = "SOLOCLEF_WORKER_FILE_STORAGE_S3_BUCKET"
	FileStorageS3Region    = "SOLOCLEF_WORKER_FILE_STORAGE_S3_REGION"
	FileStorageS3PathStyle = "SOLOCLEF_WORKER_FILE_STORAGE_S3_PATH_STYLE"
	FileStorageS3AccessKey = "SOLOCLEF_WORKER_FILE_STORAGE_S3_ACCESS_KEY_FILE"
	FileStorageS3SecretKey = "SOLOCLEF_WORKER_FILE_STORAGE_S3_SECRET_KEY_FILE"
)

// Config is the settings, read and checked.
type Config struct {
	ProxyURL    string
	ProxyKey    string
	ChatModel   string
	QuickModel  string
	CallTimeout time.Duration
	// SecretKey opens an account's own model connections (SecretKeyFile).
	SecretKey string

	Speech               speech.Service
	SpeechInstructions   string
	SpeechAttemptTimeout time.Duration
	SpeechMaxAttempts    int
	SpeechConcurrency    int
	PodcastWorkDirectory string

	QdrantURL     string
	QdrantAlias   string
	QdrantKey     string
	QdrantTimeout time.Duration

	DefaultQueue string

	FileStorage filestore.Settings
}

// Load reads the settings, or says everything that is wrong with them.
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
			problems = append(problems, fmt.Sprintf("%s=%q is not a duration greater than zero (for example 90s)", name, raw))
		}
		return parsed
	}
	count := func(name string) int {
		raw := required(name)
		if raw == "" {
			return 0
		}
		number, err := strconv.Atoi(raw)
		if err != nil || number < 1 {
			problems = append(problems, fmt.Sprintf("%s=%q is not a whole number of at least 1", name, raw))
		}
		return number
	}
	// A secret is named by the file it is in, so it is in neither the
	// process's environment nor `docker inspect`.
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
		ProxyURL:             strings.TrimRight(required(ProxyURL), "/"),
		ProxyKey:             requiredSecret(ProxyKeyFile),
		ChatModel:            required(ChatModel),
		QuickModel:           required(QuickModel),
		SecretKey:            requiredSecret(SecretKeyFile),
		CallTimeout:          duration(CallTimeout),
		SpeechInstructions:   value(SpeechInstructions),
		SpeechAttemptTimeout: duration(SpeechAttemptTimeout),
		SpeechMaxAttempts:    count(SpeechMaxAttempts),
		SpeechConcurrency:    count(SpeechConcurrency),
		PodcastWorkDirectory: required(PodcastWorkDirectory),
		QdrantURL:            strings.TrimRight(required(QdrantURL), "/"),
		QdrantAlias:          required(QdrantAlias),
		QdrantKey:            secret(QdrantKeyFile),
		QdrantTimeout:        duration(QdrantTimeout),
		DefaultQueue:         required(DefaultQueue),
	}
	if setting := required(SpeechService); setting != "" {
		service, err := speech.ReadService(setting)
		if err != nil {
			problems = append(problems, SpeechService+": "+err.Error())
		}
		config.Speech = service
	}

	// The configured backend's settings are required, and the other's are
	// read when given, so files written before a move can still be removed
	// (filestore.Stores).
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
		if config.FileStorage.S3 != (filestore.S3Settings{}) || config.FileStorage.Backend == filestore.S3Backend {
			problems = append(problems, FileStorageS3PathStyle+" is not set (true for MinIO, false for a store that addresses a bucket by host name)")
		}
	default:
		problems = append(problems, fmt.Sprintf("%s=%q must be true or false", FileStorageS3PathStyle, style))
	}

	if len(problems) > 0 {
		return Config{}, errors.New("the worker's settings are incomplete:\n  " + strings.Join(problems, "\n  "))
	}
	return config, nil
}
