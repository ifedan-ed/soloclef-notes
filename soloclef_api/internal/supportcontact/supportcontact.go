// Package supportcontact serves GET /api/v1/support-contact: the address
// "Report Issue" mails go to.
//
// It takes no sign-in. An error page or a toast can show before there is a
// session, or because there is none, and that is exactly when a person
// wants to know whom to write to. All it answers is the one address the
// operator chose to publish.
//
// The address is one setting, SOLOCLEF_API_SUPPORT_EMAIL. Left empty, the
// deployment publishes no address and the route answers {"email": ""}: a
// new server must never send its users' reports to another server's
// operator, so there is no built-in address to fall back to. The Python
// tier read the address from an administrator's setting in the billing
// store and, failing that, from the environment; billing is gone, and two
// places to look were one too many.
package supportcontact

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
)

// Path is the route.
const Path = "/api/v1/support-contact"

// Served and Public are the route as a "METHOD /path" pair. It is both:
// served, and mounted outside the gate.
func Served() []string { return []string{http.MethodGet + " " + Path} }
func Public() []string { return Served() }

// plainAddress is one bare address: nothing a mail client would read as a
// second recipient, a display name or a header. The web app builds a
// mailto: link from it, and holds the answer to the same pattern
// (lib/brand/support-email.ts).
var plainAddress = regexp.MustCompile(`^[^@\s<>"',;:?&]+@[^@\s<>"',;:?&]+\.[^@\s<>"',;:?&]+$`)

// maxAddressChars is the longest address SMTP carries.
const maxAddressChars = 254

// Route serves the address.
type Route struct {
	// Email is the published address, or empty for none.
	Email string
}

// Check refuses a setting that is not one plain address, at start-up: the
// Python tier quietly treated such a value as unset, which left an operator
// who mistyped the address publishing none without being told.
func (r Route) Check() error {
	if r.Email == "" {
		return nil
	}
	if len(r.Email) > maxAddressChars || !plainAddress.MatchString(r.Email) {
		return fmt.Errorf("supportcontact: the support address %q is not one plain email address; leave the setting empty to publish none", r.Email)
	}
	return nil
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	body, _ := json.Marshal(map[string]string{"email": r.Email})
	huma.Register(api, huma.Operation{
		OperationID: "support-contact", Method: http.MethodGet, Path: Path,
		Summary: "The address problem reports go to", Tags: []string{"support"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			response := humawriter.New(ctx)
			response.Header().Set("Content-Type", "application/json")
			// The same for everyone and changed only by a restart, so a
			// browser and the web tier's server may both keep it a minute.
			response.Header().Set("Cache-Control", "public, max-age=60")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write(body)
		}}, nil
	})
}
