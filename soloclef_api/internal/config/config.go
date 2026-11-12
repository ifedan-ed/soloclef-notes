// Package config reads this service's settings from the environment.
//
// Every value that could name a server — the address to listen on, the
// database, the Python tier — is required and has no default, because a default
// here would be this estate's own deployment written into code the repository
// ships (custom/README.md, "This server's own settings"). The auth settings
// that must agree with the Python tier mirror that tier's own defaults, so a
// deployment that leaves them unset gets the same behaviour on both tiers
// rather than a guess.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const prefix = "SOLOCLEF_API_"

type Config struct {
	// ListenAddress is host:port; a dev build is expected to bind loopback.
	ListenAddress string
	// FileStorageBackend mirrors the Python tier's FILE_STORAGE_BACKEND: the
	// backend new files are written to, "local" or "s3". Required, with no
	// default, so a replica never silently uses a store nobody named.
	//
	// It does not decide where a file is *read* from. A row records the
	// backend its bytes went to, and is read from that one
	// (internal/filestore.Stores) -- which is what keeps files written
	// before a move from "local" to "s3" readable after it. So the settings
	// of the backend that is not the configured one may be given as well,
	// and each is given whole or not at all:
	//
	//   backend   FILE_STORAGE_LOCAL_PATH        FILE_STORAGE_S3_* (all six)
	//   local     required                       optional: given, "s3" rows are read
	//   s3        optional: given, "local"       required
	//             rows are read
	//
	// A backend whose settings are absent is not read, and a row naming it
	// is a logged 500. Nothing is inferred: the settings being present never
	// changes which backend is the configured one.
	FileStorageBackend string
	// FileStorageLocalPath is where, inside this process, the volume the
	// Python tier mounts at FILE_STORAGE_LOCAL_PATH is mounted -- read-only
	// is enough.
	FileStorageLocalPath string
	// The bucket, mirroring the Python tier's six FILE_STORAGE_S3_* settings
	// one for one (app/file_storage/settings.py). None has a default. With
	// the "local" backend and none of the six set they are not read at all,
	// and the secret files are not opened. With any one of them set, all six
	// are required: a bucket described by half is somebody's mistake, and it
	// is reported at start-up rather than found later as a refused file.
	//
	// FileStorageS3Endpoint is a scheme and a host ("http://minio:9000"),
	// the scheme deciding TLS. FileStorageS3PathStyle is exactly "true"
	// (endpoint/bucket/key, MinIO's own style) or "false"
	// (bucket.endpoint/key) -- nothing looser, because any other reading
	// would be a default in disguise.
	FileStorageS3Endpoint  string
	FileStorageS3Bucket    string
	FileStorageS3Region    string
	FileStorageS3PathStyle bool
	// FileStorageS3AccessKey and FileStorageS3SecretKey are the *contents*
	// of the files named by FILE_STORAGE_S3_ACCESS_KEY_FILE and
	// FILE_STORAGE_S3_SECRET_KEY_FILE, with surrounding whitespace removed.
	// The credential is given as files so that it is a mounted secret
	// rather than a value `docker inspect` prints; there is deliberately no
	// plain-value variable beside them to fall back on.
	FileStorageS3AccessKey string
	FileStorageS3SecretKey string
	// DatabaseURL is a libpq/pgx URL for the same database the Python tier uses.
	//
	// Its password is said once, in one of two places, and the deployment
	// chooses which: inside DATABASE_URL, or in the file
	// DATABASE_PASSWORD_FILE names, which is then written into the URL here
	// (percent-encoded, so a password with "@" or "/" in it needs no care).
	// The file is for a deployment that mounts its secrets, so that the
	// password is not a value `docker inspect` prints. Giving both is
	// refused: one of the two would be ignored, and nobody could tell which
	// from outside.
	DatabaseURL string
	// DatabaseMaxConnections caps this replica's share of Postgres.
	DatabaseMaxConnections int32
	// SessionSecret is the Python tier's SECRET_KEY: the HS256 key its access
	// tokens are signed with. Nothing is verifiable without it. It is given
	// as SESSION_SECRET or as the file SESSION_SECRET_FILE names: exactly
	// one of the two, for the reason the database password is.
	SessionSecret string
	// SessionCookieName mirrors the Python tier's SESSION_COOKIE_NAME.
	SessionCookieName string
	// MinIssuedAt mirrors the Python tier's MIN_ISSUED_AT: tokens issued before
	// this unix second are refused, which is how every session is ended at once.
	MinIssuedAt int64
	// RedisURL is the Python tier's REDIS_APP_URL: the same server and the same
	// database, because the rate limit counter is shared with that tier rather
	// than kept twice (internal/ratelimit) — and because the event bus's
	// stream lives on it too, so an event published here is one the Python
	// dispatcher reads (app/event_bus/stream/publisher.py reads the same
	// setting; internal/eventstream).
	RedisURL string
	// CeleryBrokerURL is the Python tier's CELERY_BROKER_URL: the broker a
	// write's background work is enqueued on (internal/celeryqueue). It is a
	// setting of its own rather than RedisURL above, because they are two
	// settings there: the same server today, and a queue published to the
	// wrong database is work no worker will ever see.
	CeleryBrokerURL string
	// CeleryDefaultQueue is CELERY_TASK_DEFAULT_QUEUE, which the connectors
	// and heavy queue names are built from, exactly as app/celery_app.py
	// builds them.
	CeleryDefaultQueue string
	// AgentIssuer is the identity provider that signs AI agents' access tokens,
	// mirroring MCP_OAUTH_ISSUER, exactly as the provider prints it. Empty is
	// valid and means this tier verifies no agent token itself: such a request
	// is forwarded to the Python tier, which answers as it always did.
	AgentIssuer string
	// AgentClientID mirrors MCP_OAUTH_CLIENT_ID: a token is accepted only when
	// its audience is this client.
	AgentClientID string
	// AgentClientSecret is the *contents* of the file AGENT_CLIENT_SECRET_FILE
	// names: the agent application's client secret at the provider, which
	// the settings page shows a signed-in person to paste into claude.ai or
	// ChatGPT (agentsignin.SetupRoute). Nothing here verifies with it. Empty
	// is valid: a client registered as public has none.
	AgentClientSecret string
	// AgentProviderName mirrors OIDC_PROVIDER_NAME: the oauth_account.oauth_name
	// the web sign-in links identities under, so an identity linked there is
	// found here.
	AgentProviderName string
	// BrandName mirrors app.brand.BRAND_NAME, which two of the agent refusals
	// name word for word.
	BrandName string
	// PostHogAPIKey mirrors POSTHOG_API_KEY. Empty is valid and means this
	// deployment has telemetry off, as there: nothing is queued and nothing is
	// sent (internal/gatedanalytics).
	PostHogAPIKey string
	// PostHogHost mirrors POSTHOG_HOST. It is required whenever the key is set
	// and has no default, because a default here would be a vendor's address
	// written into code this repository ships.
	PostHogHost string

	// The speech service the podcasts use, mirroring the Python tier's TTS_*
	// settings of the same names (soloclef_assistant/voices). TTSService is
	// "<roster>/<model>", the model being what the LiteLLM proxy is asked
	// for; it is empty on a deployment with no podcasts. Nothing here is
	// checked: voices.New validates the four together at startup and refuses
	// to start on a wrong combination, so a bad setting is loud, not a
	// silent fallback to some other voice.
	TTSService        string
	TTSServiceAPIBase string
	TTSServiceAPIKey  string
	TTSPodcastVoices  []string
	// ErrorReportURL is app.exceptions.ISSUES_URL, which every error envelope
	// carries: this deployment's own contact page.
	ErrorReportURL string
	// AllowedOrigins is the list app.py builds from NEXT_FRONTEND_URL, so a
	// cross-origin read gets the same CORS headers back. Empty is valid: a
	// deployment with no address of its own allows only loopback, as there.
	AllowedOrigins []string
	// CSRFAllowedOrigins mirrors CSRF_ALLOWED_ORIGINS: the origins a
	// cookie-authenticated write may come from besides this deployment's own
	// two addresses and its own host (internal/csrforigin).
	CSRFAllowedOrigins []string
	// PublicURL mirrors SURFSENSE_PUBLIC_URL and FrontendURL mirrors
	// NEXT_FRONTEND_URL, which falls back to the public one there and does so
	// here. Only their origins are read, and only by the CSRF check; empty is
	// valid and contributes none, exactly as an unset one does there.
	//
	// FrontendURL is read by a response as well: every public share link's
	// public_url is this address with its trailing slashes stripped, exactly
	// as app/services/public_chat_service.py builds it
	// (internal/publiclinks).
	PublicURL   string
	FrontendURL string
	// SignupAllowed says whether an account may come to exist without the
	// operator making it: by the sign-up form (POST /auth/register) and by a
	// first single sign-on with an address no account has. Required, exactly
	// "true" or "false", with no default: whether strangers may make
	// accounts on a deployment is its operator's decision, and it is one
	// decision, so both doors read this one setting. "false" leaves
	// `soloclef-api set-password` as the only way an account is made.
	SignupAllowed bool
	// OIDCEnabled says whether people may sign in through the deployment's
	// OpenID Connect provider (internal/oidcsignin). Required, exactly
	// "true" or "false". Any provider that speaks OpenID Connect will do;
	// nothing here is behind a licence or an edition.
	//
	// The four settings below describe that sign-in and every one is
	// required when it is on; with it off none is read, and the secret's
	// file is not opened. OIDCIssuer is the provider's issuer exactly as it
	// prints it (the discovery document is read from under it).
	// OIDCClientSecret is the *contents* of the file OIDC_CLIENT_SECRET_FILE
	// names. OIDCRedirectURL is this API's own callback as the provider has
	// it registered: the public address of /auth/oidc/callback.
	//
	// FrontendURL above is required with it too: a finished or refused
	// sign-in sends the browser back to the web app.
	OIDCEnabled      bool
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string
	// The three below are what single sign-on is *called* and where the
	// phone app's sign-in may end, read only with OIDCEnabled on. They are
	// public: GET /api/v1/sign-in-options answers them to anyone, which is
	// how the phone app draws its sign-in screen (internal/signinoptions).
	//
	// OIDCButtonLabel is OIDC_BUTTON_LABEL, the words on the sign-in button.
	// It is the website's own setting (Compose gives both the same
	// SOLOCLEF_WEB_SSO_BUTTON_LABEL), so the app and the website never word
	// it differently; empty is the website's own words, "Continue with
	// single sign-on", which the app is then given too.
	//
	// OIDCDisplayName is OIDC_DISPLAY_NAME, required: the provider as a
	// sentence names it ("Example SSO can't be reached right now"). The
	// Python tier cut it out of the button's words; a name is not something
	// to guess from a label, so it is said.
	//
	// OIDCAppRedirects is OIDC_APP_REDIRECTS, the phone app's return
	// addresses (comma-separated, "soloclef://auth"): the only places an
	// app sign-in's one-time code is ever sent (internal/oidcsignin). Empty
	// is valid and means the app may not use single sign-on here; each entry
	// is an address with a scheme and nothing after its path.
	OIDCButtonLabel  string
	OIDCDisplayName  string
	OIDCAppRedirects []string
	// SMTPEnabled says whether this tier sends mail, which today is the
	// password-reset link and nothing else (internal/signin). Required,
	// exactly "true" or "false": with it off a reset is refused in so many
	// words rather than answered as if a mail were on its way.
	//
	// With it on, the host, the port, the security mode ("starttls", "tls"
	// or "none"), the sender and the timeout are all required and none has a
	// default: inferring the mode from the port is the classic source of a
	// send that hangs. SMTPUsername is optional (a local relay takes none);
	// SMTPPassword is the contents of the file SMTP_PASSWORD_FILE names and
	// is required exactly when a username is given.
	SMTPEnabled  bool
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTimeout  time.Duration
	// SupportEmail is the address "Report Issue" mails go to, which
	// GET /api/v1/support-contact answers to anyone
	// (internal/supportcontact). Empty publishes no address: a new server
	// must not send its users' reports to another server's operator, so
	// there is no built-in one.
	SupportEmail string
	// E2ERoutes says whether the end-to-end test routes under /__e2e__ are
	// mounted (internal/e2eseed). Required, exactly "true" or "false": the
	// routes hand out sessions, so whether a stack has them is said, never
	// assumed. The production compose file writes "false" as a literal.
	E2ERoutes bool
	// E2ESeedSecret is what every /__e2e__ request must carry. Read only
	// when the routes are on, and required then; it has no default.
	E2ESeedSecret string
	// RefreshCookieName mirrors REFRESH_COOKIE_NAME (same default). It is the
	// second cookie `clear_session` deletes, so a deployment that renames it
	// and does not tell this tier would leave a browser holding a refresh
	// cookie after the account was deleted
	// (internal/accountprofile.CookiePolicy).
	RefreshCookieName string
	// CookieDomain mirrors COOKIE_DOMAIN (`or None`, so empty means no Domain
	// attribute at all) and SessionCookieSameSite and
	// SessionCookieSecurePolicy mirror SESSION_COOKIE_SAMESITE and
	// SESSION_COOKIE_SECURE_POLICY, both lower-cased as that config
	// lower-cases them. All three are written into the Set-Cookie bytes the
	// account delete answers with, which is the only route of this tier that
	// writes one.
	CookieDomain              string
	SessionCookieSameSite     string
	SessionCookieSecurePolicy string
	// DisableNewAgentStack and PermissionEnabled mirror
	// SOLOCLEF_DISABLE_NEW_AGENT_STACK and SOLOCLEF_ENABLE_PERMISSION, the two
	// flags `_flag_guard` reads before it serves an agent permission rule
	// (internal/agentrules.Availability). They are read as
	// app/agents/chat/multi_agent_chat/shared/feature_flags.py reads them —
	// stripped, lower-cased, and one of `1`, `true`, `yes` or `on` — with that
	// module's own defaults, so a deployment that sets neither gets the same
	// answer on both tiers.
	//
	// One edge differs and cannot be closed from here: a variable that is
	// *present but empty* is False there and the default here, because Go's
	// environment accessor cannot tell an empty value from an unset one. Every
	// other setting in this file has the same limitation.
	DisableNewAgentStack bool
	PermissionEnabled    bool
	// The chat (internal/chat), which this tier runs itself: the model, the
	// search over indexed documents, the outside sources, and the bounds of
	// one turn. Every one of these is required unless it says otherwise, and
	// none has a default. An address or a model name written here would be
	// one deployment's choice shipped to every other, and a bound nobody
	// chose (how long a turn may run, how many steps it may take) is a bill
	// nobody agreed to.
	//
	// LiteLLMURL is the model proxy's OpenAI-compatible root, ending in /v1.
	// It is the only way this tier reaches a model: no provider is ever
	// called directly, so there is no provider URL or provider key here.
	// LiteLLMKey is the *contents* of the file LITELLM_KEY_FILE names, read
	// as the bucket's two secrets are: a mounted file, never a plain value.
	LiteLLMURL string
	LiteLLMKey string
	// ChatModel is the proxy's name for the model that answers.
	ChatModel string
	// EmbeddingModel and EmbeddingDimension must be the model the indexer
	// embedded the documents with and the length of its vectors (the
	// indexer's embedding profile). A question embedded with another model
	// would search the collection with a vector that means nothing in it,
	// and find plausible nonsense rather than fail.
	EmbeddingModel     string
	EmbeddingDimension int
	// QdrantURL is the vector store's HTTP address and QdrantChunkCollection
	// the name searches address: the alias the indexer keeps pointed at the
	// current collection. QdrantAPIKey is optional, the contents of the
	// file QDRANT_API_KEY_FILE names: Qdrant on the stack's own network runs
	// without a key, and then there is no file to name.
	QdrantURL             string
	QdrantAPIKey          string
	QdrantChunkCollection string
	// QdrantSearchHNSWEf is how many candidates a search keeps while it
	// walks the graph: recall bought with time. QdrantQuantized says the
	// collection stores quantized vectors; QdrantSearchRescore then says
	// whether the best candidates are scored again against the full vectors
	// and QdrantSearchOversampling how many times the wanted number are
	// fetched for that. The three booleans and numbers are measured choices
	// about one collection (custom/docs/vector-store.md), so they are that
	// deployment's to state: exactly "true" or "false", and a number that
	// is at least 1.
	QdrantSearchHNSWEf       int
	QdrantQuantized          bool
	QdrantSearchRescore      bool
	QdrantSearchOversampling float64
	// NCBITool names this deployment to PubMed, as NCBI asks every client
	// to. NCBIEmail and NCBIAPIKey (the contents of NCBI_API_KEY_FILE) are
	// optional: NCBI answers without them, at its lower rate.
	NCBITool   string
	NCBIEmail  string
	NCBIAPIKey string
	// ChatMaxSteps is how many steps (model calls and the tool calls they
	// ask for) one turn may take, and ChatMaxTurn how long it may run, read
	// from CHAT_MAX_TURN_SECONDS.
	ChatMaxSteps int
	ChatMaxTurn  time.Duration
	// ChatImageModel is the proxy's name for the model that draws images
	// and infographics. Optional: empty means this deployment draws none,
	// and a person who asks for one is told so.
	ChatImageModel string
	// ChatVisionModel is the proxy's name for the model that looks at
	// pictures for an account that chose none of its own: a photo attached
	// to a question, an upload's pictures, and an infographic read back to
	// check its text. Optional: empty, an account with no choice has none
	// of those looked at. A built document's pages are never shown to it:
	// they are measured (soloclef_assistant/deliverables, layout_rules.go).
	ChatVisionModel string
	// SandboxEnabled says whether reports, slides, spreadsheets and pages
	// are built, in a sandbox. Required, exactly "true" or "false": whether
	// a deployment runs people's document builds is a decision, not
	// something to fall into.
	//
	// The nine Sandbox settings describe that sandbox (an OpenSandbox
	// server), and every one is required when the switch is on. None has a
	// default: the image decides what a build can do, and the CPU, the
	// memory, the lifetimes and the number per account decide what an
	// account can cost. With the switch off they are not read at all, so
	// a deployment can keep them written down while it is switched off.
	//
	// SandboxStack (SANDBOX_STACK) is this deployment's name on every
	// sandbox it makes and in every lookup, the worker's too (its own
	// SANDBOX_STACK settings), because the server finds sandboxes among
	// every container on its Docker: the Compose project's name.
	// SandboxURL is the server's root (http://opensandbox-server:8080);
	// SandboxAPIKey is the contents of the file SANDBOX_API_KEY_FILE names.
	// SandboxCPU and SandboxMemory are in the server's own spelling ("2",
	// "6Gi"). SandboxIdleTTL (SANDBOX_IDLE_TTL_SECONDS, at least 60) is how
	// long a sandbox outlives its last use; SandboxCommandTimeout
	// (SANDBOX_COMMAND_TIMEOUT_SECONDS) the longest one command may run.
	SandboxEnabled        bool
	SandboxStack          string
	SandboxURL            string
	SandboxAPIKey         string
	SandboxImage          string
	SandboxCPU            string
	SandboxMemory         string
	SandboxIdleTTL        time.Duration
	SandboxCommandTimeout time.Duration
	SandboxMaxPerAccount  int
	// UploadMaxFileBytes is the largest file an upload, a folder upload or a
	// Nextcloud import may bring in, and UploadMaxFiles the most files one
	// upload request may carry. Both are required: how large a file a
	// deployment's store and parsers should be asked to take is its
	// operator's decision, and the capabilities route tells clients what
	// was decided.
	//
	// The parsers themselves are not read here. They are the indexer's
	// settings too (SOLOCLEF_PARSER_DEFAULT and the rest, with no
	// SOLOCLEF_API_ prefix), read by soloclef_ingest/parserconfig in both
	// processes so that the parser the API accepts for a file is one the
	// indexer can run.
	UploadMaxFileBytes int64
	UploadMaxFiles     int
	// NoteIndexDelay is how long a note's indexing job waits after the save
	// that queued it, read from NOTE_INDEX_DELAY_SECONDS (internal/notes).
	// Required, at least one second. It is a trade a deployment makes and
	// not a fact of the code: longer, and a burst of typing is fewer
	// embedding calls; shorter, and what was just written is found by the
	// chat and the search box sooner. (It was a constant, fifteen seconds,
	// which nobody running the site could see or change, and which made
	// every test that writes a note wait a quarter of a minute for it.)
	NoteIndexDelay time.Duration
	// NextcloudBaseURL is the one Nextcloud server this deployment's
	// people connect to. It is optional: a deployment with no Nextcloud
	// leaves it unset, and then the Nextcloud import routes are not
	// mounted and a removed Nextcloud connection's app password is not
	// deleted at any server. Set, it must be a plain http(s) address, and
	// NextcloudDownloadConcurrency is then required: how many files are
	// fetched from that server at once, which is a politeness limit toward
	// it as much as a limit on this process.
	NextcloudBaseURL             string
	NextcloudDownloadConcurrency int
	// The five settings connecting Google Drive needs (internal/driveconnect).
	// All optional: a deployment that sets none has no Drive, and the status
	// route then names the ones that are missing, so a half-configured one
	// is told what is left. The client ID and secret are an OAuth web
	// client's (the secret is the contents of the file
	// GOOGLE_OAUTH_CLIENT_SECRET_FILE names); the redirect URI is the one registered for it, and its path
	// must be the callback route's; the Picker key is a browser API key,
	// handed to the browser by design; the app id is the Cloud project
	// number, needed only when the client ID does not begin with it.
	GoogleOAuthClientID     string
	GoogleOAuthClientSecret string
	GoogleDriveRedirectURI  string
	GooglePickerAPIKey      string
	GooglePickerAppID       string
	// The four settings connecting a Microsoft mailbox needs
	// (internal/microsoftmail). All optional, as a group: a deployment that
	// sets none offers no Microsoft sign-in, and its routes say so. The
	// tenant has no default ("common", "organizations", "consumers", a
	// tenant id or a domain): which accounts may sign in is a decision. The
	// client secret is a file's content; the redirect URI is the one
	// registered at Microsoft, and its path is that package's callback.
	MicrosoftMailClientID     string
	MicrosoftMailClientSecret string
	MicrosoftMailTenant       string
	MicrosoftMailRedirectURI  string
	// ShutdownGrace is how long in-flight requests have after a signal.
	ShutdownGrace time.Duration
	// LogLevel is debug, info, warn or error.
	LogLevel string
}

// Load reads the environment, or reports every setting that is missing or
// unusable at once, so a misconfigured deployment fails at startup rather than
// on its first request.
func Load(env func(string) string) (Config, error) {
	var problems []string

	required := func(name string) string {
		value := strings.TrimSpace(env(prefix + name))
		if value == "" {
			problems = append(problems, prefix+name+" is required")
		}
		return value
	}
	optional := func(name, fallback string) string {
		value := strings.TrimSpace(env(prefix + name))
		if value == "" {
			return fallback
		}
		return value
	}
	number := func(name, fallback string) int64 {
		text := optional(name, fallback)
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			problems = append(problems, prefix+name+" must be a whole number")
			return 0
		}
		return value
	}

	// The three below are for settings with no default. `number` above takes
	// a fallback because the settings it reads mirror the Python tier's own
	// defaults; the chat's settings mirror nothing, so a missing one is
	// reported and a wrong one is reported with what it has to be.
	requiredWhole := func(name string) int {
		text := required(name)
		if text == "" {
			return 0
		}
		value, err := strconv.Atoi(text)
		if err != nil || value < 1 {
			problems = append(problems, prefix+name+" must be a whole number of at least 1")
			return 0
		}
		return value
	}
	// Exactly "true" or "false", as FILE_STORAGE_S3_PATH_STYLE is read below:
	// any looser reading ("1", "yes", an empty value meaning false) would be
	// a default in disguise.
	requiredSwitch := func(name string) bool {
		switch required(name) {
		case "true":
			return true
		case "false", "":
			// "" was reported as missing by `required`.
			return false
		default:
			problems = append(problems, prefix+name+" must be true or false")
			return false
		}
	}
	// A secret is named by the file it is in, so that it is a mounted secret
	// rather than a value `docker inspect` prints. An unset name is no
	// secret; a name whose file cannot be used is a problem either way,
	// because a deployment that names a file means it to be read.
	secretFile := func(name string) string {
		path := strings.TrimSpace(env(prefix + name))
		if path == "" {
			return ""
		}
		content, problem := readSecretFile(path)
		if problem != "" {
			problems = append(problems, prefix+name+" names "+strconv.Quote(path)+", which "+problem)
		}
		return content
	}
	requiredSecretFile := func(name string) string {
		if required(name) == "" {
			return ""
		}
		return secretFile(name)
	}

	// A secret that may be given as a plain value or as a file: exactly one
	// of `name` and `name`_FILE. Neither is a missing setting and both is a
	// contradiction, and each is reported as what it is. This is not a
	// fallback from one to the other: nothing is ever read from the second
	// because the first was empty.
	plainOrFile := func(name string) string {
		plain := strings.TrimSpace(env(prefix + name))
		path := strings.TrimSpace(env(prefix + name + "_FILE"))
		switch {
		case plain != "" && path != "":
			problems = append(problems, prefix+name+" and "+prefix+name+"_FILE are both set: give the secret once")
			return ""
		case path != "":
			return secretFile(name + "_FILE")
		case plain == "":
			problems = append(problems, prefix+name+" is required (or "+prefix+name+"_FILE, naming a file that holds it)")
		}
		return plain
	}
	// plainOrFile for a secret a deployment may also leave out: neither
	// given is no secret, and the other two cases are plainOrFile's.
	optionalPlainOrFile := func(name string) string {
		if strings.TrimSpace(env(prefix+name)) == "" && strings.TrimSpace(env(prefix+name+"_FILE")) == "" {
			return ""
		}
		return plainOrFile(name)
	}

	config := Config{
		ListenAddress:          required("LISTEN_ADDRESS"),
		FileStorageBackend:     required("FILE_STORAGE_BACKEND"),
		FileStorageLocalPath:   optional("FILE_STORAGE_LOCAL_PATH", ""),
		ErrorReportURL:         required("ERROR_REPORT_URL"),
		AllowedOrigins:         splitList(optional("ALLOWED_ORIGINS", "")),
		DatabaseURL:            required("DATABASE_URL"),
		SessionSecret:          plainOrFile("SESSION_SECRET"),
		RedisURL:               required("REDIS_URL"),
		CeleryBrokerURL:        required("CELERY_BROKER_URL"),
		CeleryDefaultQueue:     required("CELERY_DEFAULT_QUEUE"),
		DatabaseMaxConnections: int32(number("DATABASE_MAX_CONNECTIONS", "10")),
		// app/config: SESSION_COOKIE_NAME, MIN_ISSUED_AT.
		SessionCookieName: optional("SESSION_COOKIE_NAME", "surfsense_session"),
		AgentIssuer:       optional("AGENT_ISSUER", ""),
		AgentClientID:     optional("AGENT_CLIENT_ID", ""),
		AgentClientSecret: secretFile("AGENT_CLIENT_SECRET_FILE"),
		// app/agent_sign_in/settings.py and app/brand.py, same defaults.
		AgentProviderName: optional("AGENT_PROVIDER_NAME", "authentik"),
		BrandName:         optional("BRAND_NAME", "Soloclef Notes"),
		PostHogAPIKey:     optional("POSTHOG_API_KEY", ""),
		PostHogHost:       strings.TrimRight(optional("POSTHOG_HOST", ""), "/"),
		TTSService:        optional("TTS_SERVICE", ""),
		TTSServiceAPIBase: optional("TTS_SERVICE_API_BASE", ""),
		// The proxy's key for speech, as a value or (in a deployment that
		// mounts its secrets) as the file it is in.
		TTSServiceAPIKey: optionalPlainOrFile("TTS_SERVICE_API_KEY"),
		TTSPodcastVoices: splitList(optional("TTS_PODCAST_VOICES", "")),
		MinIssuedAt:      number("MIN_ISSUED_AT", "0"),
		// app/auth/csrf.py's _allowed_origins and app/config: the same three
		// inputs, with NEXT_FRONTEND_URL's own fallback below.
		CSRFAllowedOrigins: splitList(optional("CSRF_ALLOWED_ORIGINS", "")),
		PublicURL:          optional("PUBLIC_URL", ""),
		FrontendURL:        optional("FRONTEND_URL", ""),
		// app/config: REFRESH_COOKIE_NAME, COOKIE_DOMAIN (`or None`),
		// SESSION_COOKIE_SAMESITE and SESSION_COOKIE_SECURE_POLICY, with the
		// same defaults and the same lower-casing.
		RefreshCookieName:         optional("REFRESH_COOKIE_NAME", "surfsense_refresh"),
		CookieDomain:              optional("COOKIE_DOMAIN", ""),
		SessionCookieSameSite:     strings.ToLower(optional("SESSION_COOKIE_SAMESITE", "lax")),
		SessionCookieSecurePolicy: strings.ToLower(optional("SESSION_COOKIE_SECURE_POLICY", "auto")),
		// feature_flags.py's _env_bool, with that module's own defaults.
		DisableNewAgentStack: truth(env, "DISABLE_NEW_AGENT_STACK", false),
		PermissionEnabled:    truth(env, "ENABLE_PERMISSION", true),
		// The chat's settings (see the fields). TrimRight on the two
		// addresses for the reason POSTHOG_HOST has it: a path is appended
		// to each, and "…/v1/" would make "…/v1//chat/completions".
		LiteLLMURL:              strings.TrimRight(required("LITELLM_URL"), "/"),
		LiteLLMKey:              requiredSecretFile("LITELLM_KEY_FILE"),
		ChatModel:               required("CHAT_MODEL"),
		EmbeddingModel:          required("EMBEDDING_MODEL"),
		EmbeddingDimension:      requiredWhole("EMBEDDING_DIMENSION"),
		QdrantURL:               strings.TrimRight(required("QDRANT_URL"), "/"),
		QdrantAPIKey:            secretFile("QDRANT_API_KEY_FILE"),
		QdrantChunkCollection:   required("QDRANT_CHUNK_COLLECTION"),
		QdrantSearchHNSWEf:      requiredWhole("QDRANT_SEARCH_HNSW_EF"),
		QdrantQuantized:         requiredSwitch("QDRANT_QUANTIZED"),
		QdrantSearchRescore:     requiredSwitch("QDRANT_SEARCH_RESCORE"),
		NCBITool:                required("NCBI_TOOL"),
		NCBIEmail:               optional("NCBI_EMAIL", ""),
		NCBIAPIKey:              secretFile("NCBI_API_KEY_FILE"),
		ChatMaxSteps:            requiredWhole("CHAT_MAX_STEPS"),
		ChatMaxTurn:             time.Duration(requiredWhole("CHAT_MAX_TURN_SECONDS")) * time.Second,
		ChatImageModel:          optional("CHAT_IMAGE_MODEL", ""),
		ChatVisionModel:         optional("CHAT_VISION_MODEL", ""),
		SandboxEnabled:          requiredSwitch("SANDBOX_ENABLED"),
		SignupAllowed:           requiredSwitch("SIGNUP_ALLOWED"),
		OIDCEnabled:             requiredSwitch("OIDC_ENABLED"),
		SMTPEnabled:             requiredSwitch("SMTP_ENABLED"),
		SupportEmail:            optional("SUPPORT_EMAIL", ""),
		E2ERoutes:               requiredSwitch("E2E_ROUTES"),
		UploadMaxFiles:          requiredWhole("UPLOAD_MAX_FILES"),
		NoteIndexDelay:          time.Duration(requiredWhole("NOTE_INDEX_DELAY_SECONDS")) * time.Second,
		NextcloudBaseURL:        strings.TrimRight(optional("NEXTCLOUD_BASE_URL", ""), "/"),
		GoogleOAuthClientID:     optional("GOOGLE_OAUTH_CLIENT_ID", ""),
		GoogleOAuthClientSecret: secretFile("GOOGLE_OAUTH_CLIENT_SECRET_FILE"),
		GoogleDriveRedirectURI:  optional("GOOGLE_DRIVE_REDIRECT_URI", ""),
		GooglePickerAPIKey:      optional("GOOGLE_PICKER_API_KEY", ""),
		GooglePickerAppID:       optional("GOOGLE_PICKER_APP_ID", ""),
		ShutdownGrace:           time.Duration(number("SHUTDOWN_GRACE_SECONDS", "15")) * time.Second,
		LogLevel:                optional("LOG_LEVEL", "info"),

		MicrosoftMailClientID:     optional("MICROSOFT_MAIL_CLIENT_ID", ""),
		MicrosoftMailClientSecret: secretFile("MICROSOFT_MAIL_CLIENT_SECRET_FILE"),
		MicrosoftMailTenant:       optional("MICROSOFT_MAIL_TENANT", ""),
		MicrosoftMailRedirectURI:  optional("MICROSOFT_MAIL_REDIRECT_URI", ""),
	}

	if config.FrontendURL == "" {
		config.FrontendURL = config.PublicURL
	}

	// An http(s) address with a host, for the settings a browser or a
	// provider is sent to.
	webAddress := func(name, value string) {
		if value == "" {
			return
		}
		address, err := url.Parse(value)
		if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" {
			problems = append(problems, prefix+name+" must be an http(s) address")
		}
	}

	// Single sign-on, whole when it is switched on and unread when it is off.
	if config.OIDCEnabled {
		config.OIDCIssuer = required("OIDC_ISSUER")
		webAddress("OIDC_ISSUER", config.OIDCIssuer)
		config.OIDCClientID = required("OIDC_CLIENT_ID")
		config.OIDCClientSecret = requiredSecretFile("OIDC_CLIENT_SECRET_FILE")
		config.OIDCRedirectURL = required("OIDC_REDIRECT_URL")
		webAddress("OIDC_REDIRECT_URL", config.OIDCRedirectURL)
		config.OIDCButtonLabel = strings.Join(strings.Fields(optional("OIDC_BUTTON_LABEL", "")), " ")
		config.OIDCDisplayName = strings.Join(strings.Fields(required("OIDC_DISPLAY_NAME")), " ")
		config.OIDCAppRedirects = splitList(optional("OIDC_APP_REDIRECTS", ""))
		for _, address := range config.OIDCAppRedirects {
			parsed, err := url.Parse(address)
			if err != nil || parsed.Scheme == "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(address, " \t\r\n") {
				problems = append(problems, prefix+"OIDC_APP_REDIRECTS must list return addresses with a scheme and no query or fragment (soloclef://auth)")
				break
			}
		}
	}

	// Mail, likewise.
	if config.SMTPEnabled {
		config.SMTPHost = required("SMTP_HOST")
		config.SMTPPort = requiredWhole("SMTP_PORT")
		config.SMTPFrom = required("SMTP_FROM")
		config.SMTPTimeout = time.Duration(requiredWhole("SMTP_TIMEOUT_SECONDS")) * time.Second
		switch config.SMTPSecurity = required("SMTP_SECURITY"); config.SMTPSecurity {
		case "starttls", "tls", "none", "":
			// "" was reported as missing by `required`.
		default:
			problems = append(problems, prefix+"SMTP_SECURITY must be starttls, tls or none")
		}
		config.SMTPUsername = optional("SMTP_USERNAME", "")
		config.SMTPPassword = secretFile("SMTP_PASSWORD_FILE")
		if (config.SMTPUsername == "") != (strings.TrimSpace(env(prefix+"SMTP_PASSWORD_FILE")) == "") {
			problems = append(problems, prefix+"SMTP_USERNAME and "+prefix+"SMTP_PASSWORD_FILE are set together or not at all")
		}
	}

	// The end-to-end routes, whole when they are switched on and unread
	// when they are off. Such a stack keeps its mail in memory for the
	// suite to read, so it cannot also be told to send mail: one of the two
	// would be quietly ignored.
	if config.E2ERoutes {
		config.E2ESeedSecret = required("E2E_SEED_SECRET")
		if config.SMTPEnabled {
			problems = append(problems, prefix+"SMTP_ENABLED must be false when "+prefix+"E2E_ROUTES is true: an end-to-end stack keeps its mail and sends none")
		}
	}

	// Both send a browser to the web app: single sign-on when it finishes
	// or is refused, and the reset mail in its link. Neither can guess the
	// address.
	if (config.OIDCEnabled || config.SMTPEnabled) && config.FrontendURL == "" {
		problems = append(problems, prefix+"FRONTEND_URL is required when "+prefix+"OIDC_ENABLED or "+prefix+"SMTP_ENABLED is true")
	}
	if config.OIDCEnabled || config.SMTPEnabled {
		webAddress("FRONTEND_URL", config.FrontendURL)
	}

	// The sandbox, whole when it is switched on and unread when it is off.
	if config.SandboxEnabled {
		// Checked for its spelling by the sandbox client, at start-up too.
		config.SandboxStack = required("SANDBOX_STACK")
		config.SandboxURL = strings.TrimRight(required("SANDBOX_URL"), "/")
		if config.SandboxURL != "" {
			address, err := url.Parse(config.SandboxURL)
			if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" {
				problems = append(problems, prefix+"SANDBOX_URL must be an http(s) address (http://opensandbox-server:8080)")
			}
		}
		config.SandboxAPIKey = requiredSecretFile("SANDBOX_API_KEY_FILE")
		config.SandboxImage = required("SANDBOX_IMAGE")
		config.SandboxCPU = required("SANDBOX_CPU")
		config.SandboxMemory = required("SANDBOX_MEMORY")
		// The server refuses a time to live under a minute, and would
		// refuse it on the first build rather than at start-up.
		if seconds := requiredWhole("SANDBOX_IDLE_TTL_SECONDS"); seconds > 0 && seconds < 60 {
			problems = append(problems, prefix+"SANDBOX_IDLE_TTL_SECONDS must be at least 60")
		} else {
			config.SandboxIdleTTL = time.Duration(seconds) * time.Second
		}
		config.SandboxCommandTimeout = time.Duration(requiredWhole("SANDBOX_COMMAND_TIMEOUT_SECONDS")) * time.Second
		config.SandboxMaxPerAccount = requiredWhole("SANDBOX_MAX_PER_ACCOUNT")
	}

	// A size in bytes is past what an int holds on a 32-bit build, so it is
	// read as an int64 of its own and not through requiredWhole.
	if text := required("UPLOAD_MAX_FILE_BYTES"); text != "" {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil || value < 1 {
			problems = append(problems, prefix+"UPLOAD_MAX_FILE_BYTES must be a whole number of bytes, at least 1")
		} else {
			config.UploadMaxFileBytes = value
		}
	}

	// Nextcloud is one decision said in two settings: with a server named,
	// the concurrency is required; with none, a concurrency names nothing
	// and is somebody's half-finished configuration.
	switch {
	case config.NextcloudBaseURL != "":
		address, err := url.Parse(config.NextcloudBaseURL)
		if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" ||
			address.User != nil || address.RawQuery != "" || address.Fragment != "" {
			problems = append(problems, prefix+"NEXTCLOUD_BASE_URL must be a plain http(s) address (https://cloud.example.com), with no user, query or fragment")
		}
		config.NextcloudDownloadConcurrency = requiredWhole("NEXTCLOUD_DOWNLOAD_CONCURRENCY")
	case strings.TrimSpace(env(prefix+"NEXTCLOUD_DOWNLOAD_CONCURRENCY")) != "":
		problems = append(problems, prefix+"NEXTCLOUD_DOWNLOAD_CONCURRENCY is set and "+prefix+
			"NEXTCLOUD_BASE_URL is not: name the Nextcloud server, or remove the concurrency")
	}

	// The database password from its file, written into the URL (see
	// Config.DatabaseURL). Only when a file is named: with none, the URL is
	// used exactly as it was given.
	if strings.TrimSpace(env(prefix+"DATABASE_PASSWORD_FILE")) != "" && config.DatabaseURL != "" {
		password := secretFile("DATABASE_PASSWORD_FILE")
		address, err := url.Parse(config.DatabaseURL)
		switch {
		case err != nil || address.User == nil || address.User.Username() == "":
			// The parse error is not quoted: it repeats the URL, and a URL
			// that was given with a password in it must not reach a log.
			problems = append(problems, prefix+"DATABASE_URL must be a URL that names a user (postgres://user@host:5432/name) when "+
				prefix+"DATABASE_PASSWORD_FILE is set")
		default:
			if _, inline := address.User.Password(); inline {
				problems = append(problems, prefix+"DATABASE_URL carries a password and "+
					prefix+"DATABASE_PASSWORD_FILE is set: give the password once")
			} else if password != "" {
				address.User = url.UserPassword(address.User.Username(), password)
				config.DatabaseURL = address.String()
			}
		}
	}
	// Qdrant refuses an oversampling below 1, and would refuse it on the
	// first search rather than at start-up, so it is refused here.
	if text := required("QDRANT_SEARCH_OVERSAMPLING"); text != "" {
		value, err := strconv.ParseFloat(text, 64)
		// The comparison is written so that NaN, which ParseFloat accepts
		// and which is neither below 1 nor at least 1, is refused too.
		if err != nil || !(value >= 1) || value > 1e6 {
			problems = append(problems, prefix+"QDRANT_SEARCH_OVERSAMPLING must be a number of at least 1")
		} else {
			config.QdrantSearchOversampling = value
		}
	}

	// The backend that is written to must have its settings (the table on
	// Config.FileStorageBackend). A name that is neither backend is left for
	// internal/filestore to refuse, which knows the names.
	if config.FileStorageBackend == "local" && config.FileStorageLocalPath == "" {
		problems = append(problems, prefix+"FILE_STORAGE_LOCAL_PATH is required when "+
			prefix+"FILE_STORAGE_BACKEND is local")
	}

	// The bucket's settings: read when s3 is the configured backend, and
	// also when any one of them is set beside another backend -- which says
	// "rows recorded s3 are to be readable here", and has to say it whole.
	// They are `required` here rather than left for internal/filestore to
	// refuse one at a time, so that every missing one is in this start-up's
	// single report with its variable's name.
	bucketNamed := false
	for _, name := range []string{
		"FILE_STORAGE_S3_ENDPOINT", "FILE_STORAGE_S3_BUCKET", "FILE_STORAGE_S3_REGION",
		"FILE_STORAGE_S3_PATH_STYLE", "FILE_STORAGE_S3_ACCESS_KEY_FILE", "FILE_STORAGE_S3_SECRET_KEY_FILE",
	} {
		bucketNamed = bucketNamed || strings.TrimSpace(env(prefix+name)) != ""
	}
	if config.FileStorageBackend == "s3" || bucketNamed {
		before := len(problems)
		config.FileStorageS3Endpoint = required("FILE_STORAGE_S3_ENDPOINT")
		config.FileStorageS3Bucket = required("FILE_STORAGE_S3_BUCKET")
		config.FileStorageS3Region = required("FILE_STORAGE_S3_REGION")
		switch pathStyle := required("FILE_STORAGE_S3_PATH_STYLE"); pathStyle {
		case "true":
			config.FileStorageS3PathStyle = true
		case "false", "":
			// "" was reported as missing by `required` just above.
		default:
			problems = append(problems, prefix+"FILE_STORAGE_S3_PATH_STYLE must be true or false")
		}
		config.FileStorageS3AccessKey = requiredSecretFile("FILE_STORAGE_S3_ACCESS_KEY_FILE")
		config.FileStorageS3SecretKey = requiredSecretFile("FILE_STORAGE_S3_SECRET_KEY_FILE")
		// "X is required" alone would puzzle a deployment that named local:
		// it is told why the bucket is being asked for at all.
		if config.FileStorageBackend != "s3" && len(problems) > before {
			problems = append(problems, "the "+prefix+"FILE_STORAGE_S3_* settings are set in part, beside backend "+
				strconv.Quote(config.FileStorageBackend)+": set all six (rows recorded s3 are then read from the bucket) or none")
		}
	}

	// Half-configured agent verification would be a guess at which client a
	// token must be addressed to, and that guess is the audience check.
	if (config.AgentIssuer == "") != (config.AgentClientID == "") {
		problems = append(problems,
			prefix+"AGENT_ISSUER and "+prefix+"AGENT_CLIENT_ID are set together or not at all")
	}
	// A secret with no provider beside it is a deployment that believes agent
	// sign-in is on when it is not.
	if config.AgentClientSecret != "" && config.AgentIssuer == "" {
		problems = append(problems,
			prefix+"AGENT_CLIENT_SECRET_FILE is set without "+prefix+"AGENT_ISSUER and "+prefix+"AGENT_CLIENT_ID")
	}
	if config.PostHogAPIKey != "" && config.PostHogHost == "" {
		problems = append(problems, prefix+"POSTHOG_HOST is required when "+prefix+"POSTHOG_API_KEY is set")
	}
	if config.DatabaseMaxConnections < 1 {
		problems = append(problems, prefix+"DATABASE_MAX_CONNECTIONS must be at least 1")
	}
	switch config.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, prefix+"LOG_LEVEL must be debug, info, warn or error")
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("configuration: %s", strings.Join(problems, "; "))
	}
	return config, nil
}

// readSecretFile returns the secret a file holds, or what is wrong with the
// file in words that finish the sentence "<variable> names <path>, which
// ...". The whitespace around the content is dropped, because a secret
// written with `echo` or an editor ends in a newline that is not part of
// it. The content never appears in the problem text.
func readSecretFile(path string) (secret, problem string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "cannot be read"
	}
	secret = strings.TrimSpace(string(content))
	if secret == "" {
		return "", "is empty"
	}
	return secret, ""
}

// truth is feature_flags.py's `_env_bool`: the value stripped, lower-cased, and
// one of `1`, `true`, `yes` or `on`.
func truth(env func(string) string, name string, fallback bool) bool {
	raw := strings.TrimSpace(env(prefix + name))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// splitList reads a comma-separated setting.
func splitList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}

// FromEnvironment is Load against the real environment.
func FromEnvironment() (Config, error) {
	return Load(os.Getenv)
}
