package appupdate

import (
	"fmt"
	"regexp"
	"strings"
)

// BundleIDSetting is the app the App Store is asked about: the iPhone app's
// bundle id (such as com.example.notes). It is read only when phone pushes are on
// (SOLOCLEF_WORKER_PUSH_ENABLED=true): telling phones about an update is a
// push. Empty is a deployment with no App Store listing of its own, and no
// update check runs (the worker says so at start); there is no default, so a
// fork's worker never asks about another publisher's app. The Compose file
// requires the line, so leaving it out is a mistake Compose names rather
// than the check quietly switched off.
const BundleIDSetting = "SOLOCLEF_WORKER_APP_STORE_BUNDLE_ID"

// bundleShape is a bundle id as App Store Connect allows one: dot-separated
// parts of letters, digits and hyphens.
var bundleShape = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)

// LoadBundleID reads the setting: the bundle id, or "" for no update check.
func LoadBundleID(getenv func(string) string) (string, error) {
	bundle := strings.TrimSpace(getenv(BundleIDSetting))
	if bundle != "" && !bundleShape.MatchString(bundle) {
		return "", fmt.Errorf("%s is the iPhone app's bundle id, such as com.example.notes; got %q", BundleIDSetting, bundle)
	}
	return bundle, nil
}
