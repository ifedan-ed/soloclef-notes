package mcpserver

import (
	"fmt"
	"net/url"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// Refusal is why a form's masked value cannot stand for a stored secret.
// The API words it for the person; Field names the header or part, never a
// value.
type Refusal struct {
	// Part is "header", "auth", "url" or "client_secret".
	Part  string
	Field string
}

func (r *Refusal) Error() string {
	if r.Field != "" {
		return fmt.Sprintf("mcpserver: the masked %s %q has no stored value to stand for", r.Part, r.Field)
	}
	return "mcpserver: the masked " + r.Part + " has no stored value to stand for"
}

// Build is the stored form of settings a person sent: every secret in the
// clear is sealed now, and every mask stands for the secret stored under it
// in previous.
//
// A mask is only honoured while the address still names the same host as
// previous's. Without that rule a person could point a saved server at a
// host of their choosing and have the stored key sent there. For a server
// not saved yet (previous nil) no mask can be honoured.
//
// Nothing is opened: a kept secret is copied sealed. That is why a secret
// is sealed value by value and not as one blob, and why the settings screen
// can save an edit without this tier ever reading the key back.
//
// An OAuth sign-in survives an edit only when the address's host and the
// client are unchanged; otherwise the endpoints and tokens are dropped and
// the person signs in again, because a token issued for one server must not
// be sent to another.
func Build(seal sealedsecret.Seal, owner string, settings Settings, previous *Stored) (Stored, error) {
	sameHost := previous != nil && Host(settings.Address) != "" && Host(settings.Address) == Host(previous.URL)
	built := Stored{Transport: settings.Transport, Auth: StoredAuth{Type: settings.Auth.Kind}}
	sealNew := func(value string) (Secret, error) {
		sealed, err := seal.Seal(ownerKey(owner), value)
		return Secret{Mask: MaskOf(value), Sealed: sealed}, err
	}

	// The address.
	switch {
	case previous != nil && settings.Address == previous.URL:
		built.URL, built.URLSealed = previous.URL, previous.URLSealed
	case addressCarriesMask(settings.Address):
		return Stored{}, &Refusal{Part: "url"}
	default:
		shown, carriesKey := MaskAddress(settings.Address)
		built.URL = shown
		if carriesKey {
			sealed, err := seal.Seal(ownerKey(owner), settings.Address)
			if err != nil {
				return Stored{}, err
			}
			built.URLSealed = sealed
		}
	}

	// The extra headers.
	if len(settings.Headers) > 0 {
		built.Headers = make(map[string]Secret, len(settings.Headers))
	}
	for name, value := range settings.Headers {
		if !IsMasked(value) {
			secret, err := sealNew(value)
			if err != nil {
				return Stored{}, err
			}
			built.Headers[name] = secret
			continue
		}
		held, found := Secret{}, false
		if sameHost {
			held, found = headerNamed(previous.Headers, name)
		}
		if !found {
			return Stored{}, &Refusal{Part: "header", Field: name}
		}
		built.Headers[name] = held
	}

	// The authentication.
	auth := settings.Auth
	switch auth.Kind {
	case AuthHeader, AuthBearer:
		built.Auth.HeaderName = auth.HeaderName
		if !IsMasked(auth.Secret) {
			secret, err := sealNew(auth.Secret)
			if err != nil {
				return Stored{}, err
			}
			built.Auth.Secret = &secret
			break
		}
		// The stored key stands for the mask only where it was the same
		// kind of key, in the same header, for the same host.
		if !sameHost || previous.Auth.Type != auth.Kind || previous.Auth.Secret == nil ||
			!strings.EqualFold(previous.Auth.HeaderName, auth.HeaderName) {
			return Stored{}, &Refusal{Part: "auth"}
		}
		kept := *previous.Auth.Secret
		built.Auth.Secret = &kept
	case AuthOAuth:
		// The sign-in kept from before, when there is one for this host.
		var stored *StoredOAuth
		if sameHost && previous.Auth.Type == AuthOAuth {
			stored = previous.Auth.OAuth
		}
		// A dynamically registered client is the stored one: the form
		// shows its id but the person did not type it, and sends none.
		clientID := auth.ClientID
		if clientID == "" && stored != nil && stored.DynamicRegistration {
			clientID = stored.ClientID
		}
		var clientSecret *Secret
		switch {
		case auth.ClientSecret == "":
		case IsMasked(auth.ClientSecret):
			if stored == nil || stored.ClientSecret == nil || stored.ClientID != clientID {
				return Stored{}, &Refusal{Part: "client_secret"}
			}
			kept := *stored.ClientSecret
			clientSecret = &kept
		default:
			secret, err := sealNew(auth.ClientSecret)
			if err != nil {
				return Stored{}, err
			}
			clientSecret = &secret
		}
		if stored != nil && clientID == stored.ClientID && sameSecret(clientSecret, stored.ClientSecret) {
			kept := *stored
			built.Auth.OAuth = &kept
			break
		}
		// A new client, or a new host: nothing of the old sign-in applies.
		// An empty client id is "register dynamically" when the sign-in
		// starts.
		built.Auth.OAuth = &StoredOAuth{ClientID: auth.ClientID, ClientSecret: clientSecret}
	}
	return built, nil
}

// sameSecret reports whether two stored secrets are the same sealed value
// (or both absent): a client secret typed again is a new client.
func sameSecret(a, b *Secret) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Sealed == b.Sealed
}

// headerNamed finds a stored header by name. Header names are not
// case-sensitive, and a form may have tidied the spelling.
func headerNamed(headers map[string]Secret, name string) (Secret, bool) {
	for stored, secret := range headers {
		if strings.EqualFold(stored, name) {
			return secret, true
		}
	}
	return Secret{}, false
}

// addressCarriesMask reports whether an address holds a mask, raw or
// percent-encoded: a masked key that was edited around and has to be typed
// again, because the stored address is only kept when sent back unchanged.
func addressCarriesMask(address string) bool {
	if strings.Contains(address, Mask) {
		return true
	}
	unescaped, err := url.QueryUnescape(address)
	return err == nil && strings.Contains(unescaped, Mask)
}
