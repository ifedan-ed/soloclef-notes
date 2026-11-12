package modelchoices

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// Register mounts every route of the package over one Choices.
func Register(api huma.API, choices Choices, errors errorenvelope.Writer, logger *slog.Logger) {
	PurposeRoute{Path: QuickModelPath, Purpose: Quick, Choices: choices, Errors: errors, Logger: logger}.Register(api)
	PurposeRoute{Path: StudioModelPath, Purpose: Studio, Choices: choices, Errors: errors, Logger: logger}.Register(api)
	RolesRoute{Choices: choices, Errors: errors, Logger: logger}.Register(api)
	SetupRoute{Choices: choices, Errors: errors, Logger: logger}.Register(api)
}

// Routes is every route Register mounts as "METHOD path", for the lists
// the server keeps of what it answers.
func Routes() []string {
	return append(BodyRoutes(),
		http.MethodGet+" "+QuickModelPath,
		http.MethodGet+" "+StudioModelPath,
		http.MethodGet+" "+RolesPath,
		http.MethodGet+" "+SetupStatusPath,
	)
}

// BodyRoutes is the ones of them that read a body.
func BodyRoutes() []string {
	return []string{
		http.MethodPut + " " + QuickModelPath,
		http.MethodPut + " " + StudioModelPath,
		http.MethodPut + " " + RolesPath,
	}
}
