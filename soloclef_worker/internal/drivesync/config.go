package drivesync

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The settings. Every one is required when the sync is enabled; none has
// a default, for the reason internal/workerconfig gives.
const (
	// Enabled is "true" or "false". False is a deployment whose API has no
	// Google project (its Drive routes answer 503): nothing is synced and
	// no tick is scheduled. It must be said, not inferred from a missing
	// setting.
	Enabled = "SOLOCLEF_WORKER_GOOGLE_DRIVE_ENABLED"

	// ClientID and ClientSecretFile are the Google project the API's
	// SOLOCLEF_API_GOOGLE_OAUTH_CLIENT_ID and ..._CLIENT_SECRET_FILE name:
	// a grant is renewed by the project it was made for.
	ClientID         = "SOLOCLEF_WORKER_GOOGLE_OAUTH_CLIENT_ID"
	ClientSecretFile = "SOLOCLEF_WORKER_GOOGLE_OAUTH_CLIENT_SECRET_FILE"
	// SecretKeyFile is the file holding the deployment's secret key, the
	// API's SOLOCLEF_API_SESSION_SECRET: it is what the API sealed each
	// grant's tokens with, and what opens them.
	SecretKeyFile = "SOLOCLEF_WORKER_SECRET_KEY_FILE"
	// MaxFileBytes is the largest file kept, the API's
	// SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES: an imported file is an upload by
	// another road.
	MaxFileBytes = "SOLOCLEF_WORKER_GOOGLE_DRIVE_MAX_FILE_BYTES"
	// Concurrency is how many files one sync reads from Drive at once.
	Concurrency = "SOLOCLEF_WORKER_GOOGLE_DRIVE_CONCURRENCY"
)

// Google's two addresses the sync talks to. They are constants here and
// fields on the job so that a test can stand in for Google.
const (
	GoogleTokenURL = "https://oauth2.googleapis.com/token"
	GoogleDriveURL = "https://www.googleapis.com/drive/v3"
)

// Config is the Drive settings, read and checked.
type Config struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	SecretKey    string
	MaxFileBytes int64
	Concurrency  int
}

// LoadConfig reads the settings, or says everything that is wrong with
// them.
func LoadConfig(getenv func(string) string) (Config, error) {
	value := func(name string) string { return strings.TrimSpace(getenv(name)) }
	switch value(Enabled) {
	case "false":
		return Config{}, nil
	case "true":
	default:
		return Config{}, fmt.Errorf("%s must be true or false; got %q", Enabled, value(Enabled))
	}

	var problems []string
	required := func(name string) string {
		found := value(name)
		if found == "" {
			problems = append(problems, name+" is not set")
		}
		return found
	}
	// A secret is named by the file it is in, so it is in neither the
	// process's environment nor `docker inspect`.
	secret := func(name string) string {
		path := required(name)
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
	whole := func(name string) int64 {
		raw := required(name)
		if raw == "" {
			return 0
		}
		number, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || number < 1 {
			problems = append(problems, fmt.Sprintf("%s=%q is not a whole number of at least 1", name, raw))
		}
		return number
	}

	config := Config{
		Enabled:      true,
		ClientID:     required(ClientID),
		ClientSecret: secret(ClientSecretFile),
		SecretKey:    secret(SecretKeyFile),
		MaxFileBytes: whole(MaxFileBytes),
	}
	config.Concurrency = int(whole(Concurrency))
	if len(problems) > 0 {
		return Config{}, errors.New("the Google Drive sync's settings are incomplete:\n  " + strings.Join(problems, "\n  "))
	}
	return config, nil
}
