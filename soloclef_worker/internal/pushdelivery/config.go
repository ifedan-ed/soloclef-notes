// Package pushdelivery is the worker's half of notifications: its settings,
// the Valkey reads the delivery needs (presence, a fence) in the worker's
// own Redis client, the notifier the jobs call when they finish, and the
// two delivery jobs (soloclef_notify/delivery) registered in the job
// table.
package pushdelivery

import (
	"errors"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_notify/apns"
	"git.danvics.com/danvics/soloclef/soloclef_notify/delivery"
)

// The settings.
const (
	// Enabled is exactly true or false. False writes every inbox row as
	// ever and pushes nothing: each delivery message is acknowledged and
	// logged. A development stack, or a deployment with no phone app,
	// sets false.
	Enabled = "SOLOCLEF_WORKER_PUSH_ENABLED"
	// PublicURL is the address phones are set to (the API's public
	// address). Required when Enabled is true: a push names its server so
	// the app can ignore one from a server it is no longer set to.
	PublicURL = "SOLOCLEF_WORKER_PUBLIC_URL"
	// ExpoAccessToken is the Expo access token, needed only when the Expo
	// project has "enhanced push security" switched on. It is one optional
	// setting: absent or empty means that security is off and no token is
	// sent. Present means it is on and the token is sent with every
	// request. Nothing else turns it on or off.
	ExpoAccessToken = "SOLOCLEF_WORKER_EXPO_ACCESS_TOKEN"

	// Apple's push service, which iPhones registered with an APNs token are
	// pushed through directly. The five are set together or not at all:
	// none set means this worker has no Apple key, and those iPhones are
	// not pushed (said at start and by every delivery that leaves one out);
	// some set is a mistake and the worker does not start.
	//
	// APNsKeyFile is the path of the .p8 key Apple issued (a secret file
	// mount, read once at start).
	APNsKeyFile = "SOLOCLEF_WORKER_APNS_KEY_FILE"
	// APNsKeyID is the key's id: ten characters, in its file name
	// (AuthKey_<id>.p8).
	APNsKeyID = "SOLOCLEF_WORKER_APNS_KEY_ID"
	// APNsTeamID is the Apple developer team the key belongs to.
	APNsTeamID = "SOLOCLEF_WORKER_APNS_TEAM_ID"
	// APNsTopic is the app's bundle id, such as com.example.notes.
	APNsTopic = "SOLOCLEF_WORKER_APNS_TOPIC"
	// APNsEnvironment is exactly production or sandbox: the APNs host
	// pushed, and the tokens pushed. App Store and TestFlight builds mint
	// production tokens, development builds sandbox ones; a token is pushed
	// only by a worker of its own environment, and the others are left
	// alone (not deleted).
	APNsEnvironment = "SOLOCLEF_WORKER_APNS_ENVIRONMENT"
)

// APNsConfig is Apple's settings; the zero value is "not set".
type APNsConfig struct {
	KeyFile, KeyID, TeamID, Topic, Environment string
}

// Set reports whether the deployment has an Apple key.
func (a APNsConfig) Set() bool { return a.KeyFile != "" }

// Config is the push settings.
type Config struct {
	Enabled bool
	// Origin is PublicURL as the phone app compares it.
	Origin      string
	AccessToken string
	// APNs is unset when the deployment has no Apple key.
	APNs APNsConfig
}

// LoadConfig reads the settings and reports every problem at once.
func LoadConfig(getenv func(string) string) (Config, error) {
	value := func(name string) string { return strings.TrimSpace(getenv(name)) }
	switch value(Enabled) {
	case "false":
		return Config{}, nil
	case "true":
	default:
		return Config{}, fmt.Errorf("%s must be true or false; got %q", Enabled, value(Enabled))
	}
	address := value(PublicURL)
	if address == "" {
		return Config{}, fmt.Errorf("%s is not set: with %s=true a push names its server, and the phone app ignores one that names another", PublicURL, Enabled)
	}
	origin, err := delivery.ServerOrigin(address)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", PublicURL, err)
	}
	apple, err := loadAPNs(value)
	if err != nil {
		return Config{}, err
	}
	return Config{Enabled: true, Origin: origin, AccessToken: value(ExpoAccessToken), APNs: apple}, nil
}

// loadAPNs reads Apple's five settings: all of them, or none. The key id and
// team id are checked when the key is read (apns.NewSigner), which says
// what is wrong with them.
func loadAPNs(value func(string) string) (APNsConfig, error) {
	apple := APNsConfig{
		KeyFile: value(APNsKeyFile), KeyID: value(APNsKeyID), TeamID: value(APNsTeamID),
		Topic: value(APNsTopic), Environment: value(APNsEnvironment),
	}
	names := []string{APNsKeyFile, APNsKeyID, APNsTeamID, APNsTopic, APNsEnvironment}
	values := []string{apple.KeyFile, apple.KeyID, apple.TeamID, apple.Topic, apple.Environment}
	var missing []string
	for i, name := range names {
		if values[i] == "" {
			missing = append(missing, name)
		}
	}
	switch {
	case len(missing) == len(names):
		return APNsConfig{}, nil
	case len(missing) > 0:
		return APNsConfig{}, fmt.Errorf("the APNs settings are set together or not at all; %s not set", strings.Join(missing, ", "))
	}
	if _, err := apns.URLFor(apple.Environment); err != nil {
		return APNsConfig{}, fmt.Errorf("%s: %w", APNsEnvironment, err)
	}
	if strings.ContainsAny(apple.Topic, " /") {
		return APNsConfig{}, errors.New(APNsTopic + " is the app's bundle id, such as com.example.notes")
	}
	return apple, nil
}
