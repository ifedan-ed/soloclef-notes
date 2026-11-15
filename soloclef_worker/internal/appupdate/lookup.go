package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_notify/appversion"
)

// LookupURL is Apple's public lookup. It is a constant here and a field on
// AppStore so that a test can stand in for Apple.
//
// No `country` is named, so Apple answers from the United States storefront.
// Soloclef is listed there (checked 2026-11-13), and a version number is the
// same in every storefront, so one storefront answers for all.
const LookupURL = "https://itunes.apple.com/lookup"

// Release is what the App Store offers.
type Release struct {
	Version appversion.Version
	// TrackID is the App Store's id for the app.
	TrackID int64
}

// StoreURL is the listing's address without a storefront, which Apple
// sends to the person's own storefront (and an iPhone hands to the App
// Store app). Not the lookup's trackViewUrl: that names the United States
// storefront the lookup read, which is not every person's.
func (r Release) StoreURL() string {
	return "https://apps.apple.com/app/id" + strconv.FormatInt(r.TrackID, 10)
}

// ErrNotListed is an answer that names no app: the bundle id is not in the
// App Store (yet, or any more).
var ErrNotListed = errors.New("the App Store lists no app with this bundle id")

// maxAnswer bounds the answer read: one app's listing is a few kilobytes,
// most of it the description and screenshot addresses.
const maxAnswer = 1 << 20

// AppStore is Store over Apple's lookup.
type AppStore struct {
	// BaseURL is LookupURL, or a test's stand-in.
	BaseURL  string
	BundleID string
	// HTTP bounds the request (its Timeout).
	HTTP *http.Client
}

// Check reports what is missing. Called once at start-up.
func (a AppStore) Check() error {
	if a.BaseURL == "" || a.BundleID == "" || a.HTTP == nil || a.HTTP.Timeout <= 0 {
		return errors.New("appupdate: the App Store lookup needs its address, the bundle id and a client with a timeout")
	}
	return nil
}

// lookupAnswer is the part of Apple's answer read here:
// {"resultCount": 1, "results": [{"bundleId": …, "version": "1.2.0",
// "trackId": 6821393434, …}]}.
type lookupAnswer struct {
	ResultCount int `json:"resultCount"`
	Results     []struct {
		BundleID string `json:"bundleId"`
		Version  string `json:"version"`
		TrackID  int64  `json:"trackId"`
	} `json:"results"`
}

// Lookup asks Apple which version the App Store offers. Every answer that
// is not one plain listing of this app is an error saying what it was, never
// a guess: a version that is not a dotted number, an answer about another
// app, an id that is not one.
func (a AppStore) Lookup(ctx context.Context) (Release, error) {
	address := a.BaseURL + "?bundleId=" + url.QueryEscape(a.BundleID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return Release{}, fmt.Errorf("appupdate: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := a.HTTP.Do(request)
	if err != nil {
		return Release{}, fmt.Errorf("appupdate: asking the App Store: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("appupdate: the App Store answered %d", response.StatusCode)
	}
	// Apple answers text/javascript, so the type is not checked; the body
	// is, as JSON.
	var answer lookupAnswer
	if err := json.NewDecoder(io.LimitReader(response.Body, maxAnswer)).Decode(&answer); err != nil {
		return Release{}, fmt.Errorf("appupdate: the App Store's answer does not read: %w", err)
	}
	if answer.ResultCount == 0 || len(answer.Results) == 0 {
		return Release{}, fmt.Errorf("appupdate: %s: %w", a.BundleID, ErrNotListed)
	}
	listing := answer.Results[0]
	if listing.BundleID != a.BundleID {
		return Release{}, fmt.Errorf("appupdate: the App Store answered about %q, not %q", listing.BundleID, a.BundleID)
	}
	version, err := appversion.Parse(listing.Version)
	if err != nil {
		return Release{}, fmt.Errorf("appupdate: the App Store's version %q: %w", listing.Version, err)
	}
	if listing.TrackID < 1 {
		return Release{}, fmt.Errorf("appupdate: the App Store's answer has no app id")
	}
	return Release{Version: version, TrackID: listing.TrackID}, nil
}
