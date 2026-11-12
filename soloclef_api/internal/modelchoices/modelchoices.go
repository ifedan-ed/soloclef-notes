// Package modelchoices is which model does each of an account's jobs other
// than answering its chat, and whether the account can chat at all.
//
//	GET, PUT /api/v1/users/me/quick-model       small helper calls
//	GET, PUT /api/v1/users/me/studio-model      long written work
//	GET, PUT /api/v1/users/me/model-roles       looking at pictures, drawing them
//	GET      /api/v1/users/me/llm-setup-status  is there a chat model to use
//
// # The rule
//
// It is the chat model's rule (internal/chat/models.go), applied to four
// more jobs. The deployment's models are reached through the model proxy,
// so what an account may choose is a name the proxy lists, stored as the
// proxy spells it, or a model of the account's own OpenAI-compatible
// connection that does the job, stored as "own:<n>" and called directly
// (soloclef_assistant/ownmodels). "me" is the request's principal. A choice is
// one row of `user_models`; no row means the default, and each answer names
// that default so the screen never has to guess what "default" is today.
//
// # What the default is, job by job
//
//   - quick and studio: the model that answers the account's chat (its own
//     chat choice, else the deployment's CHAT_MODEL). An account that picked
//     a chat model gets that model's voice in its podcasts and its checks
//     without picking it three times.
//   - vision and image: the deployment's CHAT_VISION_MODEL and
//     CHAT_IMAGE_MODEL. Either may be unset, and then the default is null:
//     the deployment does that job only for an account that chose a model.
//
// # What replaced the Python tier's shapes
//
// The Python routes stored integer ids of `models` rows, with 0 for "auto"
// and negative ids for the operator's file, and the studio setting carried a
// fallback model per output type that was tried when the first one failed.
// None of that is here. An id is a proxy name. There is no fallback model:
// a job whose model fails says so, because an output quietly written by a
// model nobody chose is a wrong answer that looks right.
//
// # Who reads a choice
//
// A stored choice that nothing reads is a setting that lies, so each
// purpose is listed with its reader:
//
//   - quick: internal/instructionslint (the model pass of the check).
//   - studio: soloclef_worker/internal/llm (a podcast's transcript and a
//     video's script).
//   - vision, image: stored and answered here; the chat's picture tools are
//     built once at start-up around the deployment's two names
//     (internal/chat/wire.go) and do not read an account's choice yet.
package modelchoices

// The paths.
const (
	QuickModelPath  = "/api/v1/users/me/quick-model"
	StudioModelPath = "/api/v1/users/me/studio-model"
	RolesPath       = "/api/v1/users/me/model-roles"
	SetupStatusPath = "/api/v1/users/me/llm-setup-status"
)

// Purpose is a job an account chooses a model for: the value of
// `user_models.purpose`, which the table's CHECK lists.
type Purpose string

// The purposes. Chat is read here (it is the default of two others, and
// what the setup status is about) and written only by its own route.
const (
	Chat   Purpose = "chat"
	Quick  Purpose = "quick"
	Studio Purpose = "studio"
	Vision Purpose = "vision"
	Image  Purpose = "image"
)

// maxModelNameChars bounds a name before the proxy's list is asked about
// it; proxy model names are far shorter.
const maxModelNameChars = 255
