package automationrun

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// The settings of automations. Enabled is always required; the rest are
// read only when it is true, and then every one without "optional" below is
// required. There are no defaults: a model, an address or a limit nobody
// chose is not used.
//
// Each of the search and source settings must say what the API tier's does
// (its SOLOCLEF_API_* twin, named beside it), because a run searches the
// index the chat searches, with the embedding model the documents were
// indexed with. The compose file feeds both tiers from one variable each.
const (
	// Enabled is "true" or "false". False keeps the job that ends every
	// run with a sentence saying automations are off here, starts no tick
	// and reads no events.
	Enabled = "SOLOCLEF_WORKER_AUTOMATIONS_ENABLED"
	// AppRedisURL is the API tier's Redis (SOLOCLEF_API_REDIS_URL): where
	// domain events are published and where a change to an account's
	// connected apps is counted. It need not be the broker's.
	AppRedisURL = "SOLOCLEF_WORKER_AUTOMATIONS_APP_REDIS_URL"
	// The embedding of a search's query (SOLOCLEF_API_EMBEDDING_MODEL and
	// _DIMENSION).
	EmbeddingModel     = "SOLOCLEF_WORKER_AUTOMATIONS_EMBEDDING_MODEL"
	EmbeddingDimension = "SOLOCLEF_WORKER_AUTOMATIONS_EMBEDDING_DIMENSION"
	// The collection searches read and how they read it
	// (SOLOCLEF_API_QDRANT_CHUNK_COLLECTION, _SEARCH_HNSW_EF, _QUANTIZED,
	// _SEARCH_RESCORE, _SEARCH_OVERSAMPLING). The server's address and key
	// are the worker's own SOLOCLEF_WORKER_QDRANT_URL and _API_KEY_FILE.
	QdrantCollection   = "SOLOCLEF_WORKER_AUTOMATIONS_QDRANT_COLLECTION"
	QdrantHNSWEf       = "SOLOCLEF_WORKER_AUTOMATIONS_QDRANT_HNSW_EF"
	QdrantQuantized    = "SOLOCLEF_WORKER_AUTOMATIONS_QDRANT_QUANTIZED"
	QdrantRescore      = "SOLOCLEF_WORKER_AUTOMATIONS_QDRANT_RESCORE"
	QdrantOversampling = "SOLOCLEF_WORKER_AUTOMATIONS_QDRANT_OVERSAMPLING"
	// How this deployment names itself to PubMed (SOLOCLEF_API_NCBI_TOOL);
	// the address and the key file are optional.
	NCBITool       = "SOLOCLEF_WORKER_AUTOMATIONS_NCBI_TOOL"
	NCBIEmail      = "SOLOCLEF_WORKER_AUTOMATIONS_NCBI_EMAIL"
	NCBIAPIKeyFile = "SOLOCLEF_WORKER_AUTOMATIONS_NCBI_API_KEY_FILE"
	// Brand is the product's name in the prompt (SOLOCLEF_API_BRAND_NAME).
	Brand = "SOLOCLEF_WORKER_AUTOMATIONS_BRAND"
	// MaxSteps is how many steps one step's turn may take
	// (SOLOCLEF_API_CHAT_MAX_STEPS).
	MaxSteps = "SOLOCLEF_WORKER_AUTOMATIONS_MAX_STEPS"

	// SecretKeyFile is the file holding the deployment's secret key, the
	// API's SOLOCLEF_API_SESSION_SECRET: it is what the API sealed an
	// account's mailbox passwords and Drive grant with, and what a run's
	// mail and Drive tools open them with.
	SecretKeyFile = "SOLOCLEF_WORKER_SECRET_KEY_FILE"
	// The Microsoft app registration a Microsoft mailbox's token is
	// refreshed at (SOLOCLEF_API_MICROSOFT_MAIL_CLIENT_ID,
	// _CLIENT_SECRET_FILE and _TENANT). Optional as a whole: with none of
	// the three set, a run that reads a Microsoft mailbox is told
	// Microsoft mail is not set up here. Set in part is an error.
	MicrosoftMailClientID         = "SOLOCLEF_WORKER_AUTOMATIONS_MICROSOFT_MAIL_CLIENT_ID"
	MicrosoftMailClientSecretFile = "SOLOCLEF_WORKER_AUTOMATIONS_MICROSOFT_MAIL_CLIENT_SECRET_FILE"
	MicrosoftMailTenant           = "SOLOCLEF_WORKER_AUTOMATIONS_MICROSOFT_MAIL_TENANT"
	// The Google project a Drive grant is renewed at, the one the Drive
	// sync names (internal/drivesync). Optional as a whole: with neither
	// set no run is offered the Drive tools. Set in part is an error.
	GoogleClientID         = "SOLOCLEF_WORKER_GOOGLE_OAUTH_CLIENT_ID"
	GoogleClientSecretFile = "SOLOCLEF_WORKER_GOOGLE_OAUTH_CLIENT_SECRET_FILE"
	// NextcloudURL is the deployment's Nextcloud (SOLOCLEF_API_NEXTCLOUD_BASE_URL),
	// the one server an account's stored app password is sent to. Optional:
	// empty, no run is offered the Nextcloud tools. Set, it must be a plain
	// http(s) address, as the API requires of it.
	NextcloudURL = "SOLOCLEF_WORKER_AUTOMATIONS_NEXTCLOUD_BASE_URL"

	// What a run makes artifacts with, each the API tier's own setting for
	// the chat's specialist, named beside it, because an artifact a run
	// makes is the artifact a chat makes.
	//
	// ImageModel (SOLOCLEF_API_CHAT_IMAGE_MODEL) and VisionModel
	// (SOLOCLEF_API_CHAT_VISION_MODEL) are optional: empty, a run that is
	// asked for a picture says this server draws none, and no other model
	// is tried. A built document needs no vision model: it is measured.
	ImageModel  = "SOLOCLEF_WORKER_AUTOMATIONS_IMAGE_MODEL"
	VisionModel = "SOLOCLEF_WORKER_AUTOMATIONS_VISION_MODEL"
	// PodcastVoices (SOLOCLEF_API_TTS_PODCAST_VOICES) is the voices a new
	// podcast's speakers are proposed with, comma-separated. Optional.
	PodcastVoices = "SOLOCLEF_WORKER_AUTOMATIONS_TTS_PODCAST_VOICES"
	// SandboxEnabled (SOLOCLEF_API_SANDBOX_ENABLED) is "true" or "false":
	// whether a run can build reports, slides, spreadsheets and pages. It
	// must be said. True, the nine settings after it are required
	// (SOLOCLEF_API_SANDBOX_URL and the rest).
	SandboxEnabled        = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_ENABLED"
	SandboxStack          = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_STACK"
	SandboxURL            = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_URL"
	SandboxAPIKeyFile     = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_API_KEY_FILE"
	SandboxImage          = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_IMAGE"
	SandboxCPU            = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_CPU"
	SandboxMemory         = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_MEMORY"
	SandboxIdleTTL        = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_IDLE_TTL_SECONDS"
	SandboxCommandTimeout = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_COMMAND_TIMEOUT_SECONDS"
	SandboxMaxPerAccount  = "SOLOCLEF_WORKER_AUTOMATIONS_SANDBOX_MAX_PER_ACCOUNT"
)

// Config is the settings, read.
type Config struct {
	Enabled bool

	AppRedisURL        string
	EmbeddingModel     string
	EmbeddingDimension int
	QdrantCollection   string
	QdrantHNSWEf       int
	QdrantQuantized    bool
	QdrantRescore      bool
	QdrantOversampling float64
	NCBITool           string
	NCBIEmail          string
	NCBIAPIKey         string
	Brand              string
	MaxSteps           int

	SecretKey string
	// MicrosoftMail and GoogleDrive are each all set or all empty.
	MicrosoftMailClientID     string
	MicrosoftMailClientSecret string
	MicrosoftMailTenant       string
	GoogleClientID            string
	GoogleClientSecret        string
	// NextcloudURL is "" or a server address NormalizeServerURL accepts.
	NextcloudURL string

	ImageModel     string
	VisionModel    string
	PodcastVoices  []string
	SandboxEnabled bool
	// Sandbox is read only when SandboxEnabled.
	Sandbox sandbox.Settings
}

// LoadConfig reads the settings and reports everything wrong at once.
func LoadConfig(getenv func(string) string) (Config, error) {
	var c Config
	switch getenv(Enabled) {
	case "true":
		c.Enabled = true
	case "false":
		return c, nil
	default:
		return c, fmt.Errorf("%s must be true or false, got %q", Enabled, getenv(Enabled))
	}
	var problems []string
	text := func(name string) string {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			problems = append(problems, name+" is not set")
		}
		return value
	}
	whole := func(name string) int {
		value, err := strconv.Atoi(text(name))
		if err != nil || value < 1 {
			problems = append(problems, name+" must be a whole number of at least 1")
		}
		return value
	}
	truth := func(name string) bool {
		value := getenv(name)
		if value != "true" && value != "false" {
			problems = append(problems, name+" must be true or false")
		}
		return value == "true"
	}
	c.AppRedisURL = text(AppRedisURL)
	c.EmbeddingModel, c.EmbeddingDimension = text(EmbeddingModel), whole(EmbeddingDimension)
	c.QdrantCollection, c.QdrantHNSWEf = text(QdrantCollection), whole(QdrantHNSWEf)
	if c.QdrantQuantized = truth(QdrantQuantized); c.QdrantQuantized {
		// How a search uses quantized vectors matters only when the
		// collection holds them.
		c.QdrantRescore = truth(QdrantRescore)
		oversampling, err := strconv.ParseFloat(text(QdrantOversampling), 64)
		if err != nil || oversampling < 1 {
			problems = append(problems, QdrantOversampling+" must be a number of at least 1")
		}
		c.QdrantOversampling = oversampling
	}
	c.NCBITool, c.NCBIEmail = text(NCBITool), strings.TrimSpace(getenv(NCBIEmail))
	if path := strings.TrimSpace(getenv(NCBIAPIKeyFile)); path != "" {
		key, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, NCBIAPIKeyFile+": "+err.Error())
		}
		c.NCBIAPIKey = strings.TrimSpace(string(key))
	}
	c.Brand, c.MaxSteps = text(Brand), whole(MaxSteps)

	// A secret is named by the file it is in, so it is in neither the
	// process's environment nor `docker inspect`.
	secret := func(name string, required bool) string {
		path := strings.TrimSpace(getenv(name))
		if path == "" {
			if required {
				problems = append(problems, name+" is not set")
			}
			return ""
		}
		held, err := os.ReadFile(path)
		value := strings.TrimSpace(string(held))
		switch {
		case err != nil:
			problems = append(problems, name+": "+err.Error())
		case value == "":
			problems = append(problems, name+" names an empty file")
		}
		return value
	}
	optional := func(name string) string { return strings.TrimSpace(getenv(name)) }
	// together reports a group that is set in part: each is used whole or
	// not at all, and half a registration is a mistake to say, not a
	// feature to switch off quietly.
	together := func(what string, names []string, values ...string) {
		set := 0
		for _, value := range values {
			if value != "" {
				set++
			}
		}
		if set != 0 && set != len(values) {
			problems = append(problems, what+" is set in part: set all of "+strings.Join(names, ", ")+", or none")
		}
	}
	c.SecretKey = secret(SecretKeyFile, true)
	c.MicrosoftMailClientID, c.MicrosoftMailTenant = optional(MicrosoftMailClientID), optional(MicrosoftMailTenant)
	c.MicrosoftMailClientSecret = secret(MicrosoftMailClientSecretFile, false)
	together("Microsoft mail", []string{MicrosoftMailClientID, MicrosoftMailClientSecretFile, MicrosoftMailTenant},
		c.MicrosoftMailClientID, c.MicrosoftMailClientSecret, c.MicrosoftMailTenant)
	c.GoogleClientID, c.GoogleClientSecret = optional(GoogleClientID), secret(GoogleClientSecretFile, false)
	together("The Google project", []string{GoogleClientID, GoogleClientSecretFile}, c.GoogleClientID, c.GoogleClientSecret)
	if c.NextcloudURL = optional(NextcloudURL); c.NextcloudURL != "" {
		if _, ok := nextcloud.NormalizeServerURL(c.NextcloudURL); !ok {
			problems = append(problems, NextcloudURL+" must be a plain http(s) address, or empty for no Nextcloud")
		}
	}

	c.ImageModel, c.VisionModel = optional(ImageModel), optional(VisionModel)
	for _, voice := range strings.Split(getenv(PodcastVoices), ",") {
		if voice = strings.TrimSpace(voice); voice != "" {
			c.PodcastVoices = append(c.PodcastVoices, voice)
		}
	}
	if c.SandboxEnabled = truth(SandboxEnabled); c.SandboxEnabled {
		seconds := func(name string) time.Duration { return time.Duration(whole(name)) * time.Second }
		c.Sandbox = sandbox.Settings{
			Stack: text(SandboxStack),
			URL:   strings.TrimRight(text(SandboxURL), "/"), APIKey: secret(SandboxAPIKeyFile, true),
			Image: text(SandboxImage), CPU: text(SandboxCPU), Memory: text(SandboxMemory),
			IdleTTL: seconds(SandboxIdleTTL), CommandTimeout: seconds(SandboxCommandTimeout),
			MaxPerAccount: whole(SandboxMaxPerAccount),
		}
	}
	if len(problems) > 0 {
		return Config{}, errors.New("automations: " + strings.Join(problems, "; "))
	}
	return c, nil
}
