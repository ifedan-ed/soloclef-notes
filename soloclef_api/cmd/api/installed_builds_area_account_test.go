package main

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"

func init() { registerArea(accountArea) }

// The account and its settings as the installed builds use them: the
// server check and sign-in, the profile, the More tab's settings screens
// (instructions, models, API keys, prompts, memory, the activity log,
// notifications), the administrator's screens, the browser hand-offs, and
// what runs in the background on every start (push registration, the
// presence beat, crash reports). Sessions are ended in the sign-out area,
// last of all. Ids 2000-2999.
//
// Alice is made an administrator here: the admin screens (model
// providers and connections) are only reached by one, and their success
// is what the builds read. Nothing in another area depends on her not
// being one.
// accountModelProxy is the service the model routes need and this test does not run.
const accountModelProxy = "the model proxy"

// accountHandoffWhy is what the builds do when a hand-off cannot start: the
// refusal is thrown with the server's sentence (runHandoff, startDriveHandoff).
const accountHandoffWhy = "the service is not set up on this server; the app shows the server's sentence"

// accountModelProxyWhy is what the builds do with a model route's 503: the
// Models screen shows the server's sentence and a retry.
const accountModelProxyWhy = "the model proxy cannot be reached here; the screen shows the server's sentence and a retry"

var accountArea = contractArea{
	Name:  "account",
	Order: 20,
	Seed: `
UPDATE "user" SET is_superuser = true WHERE id = '` + testpostgres.Alice + `';
INSERT INTO logs (id, user_id, level, status, message, source, created_at) VALUES
 (2001, '` + testpostgres.Alice + `', 'ERROR', 'FAILED', 'Indexing Tides.pdf failed', 'document_processor', now() - interval '1 hour'),
 (2002, '` + testpostgres.Alice + `', 'INFO', 'SUCCESS', 'Indexed Moon.pdf', 'document_processor', now() - interval '2 hours');
INSERT INTO connections (id, provider, base_url, scope, enabled, user_id) VALUES
 (2010, 'openai_compatible', 'http://127.0.0.1:9/v1', 'user', true, '` + testpostgres.Alice + `');
INSERT INTO notebooks (id, user_id, title) VALUES (2070, '` + testpostgres.Alice + `', 'Tides');`,
	Cases: []contractCase{
		// Change server / first start (features/server/serverCheck.ts): asked
		// before anyone is signed in.
		{Operation: "GET /api/v1/sign-in-options", Path: "/api/v1/sign-in-options", Status: 200, Anonymous: true},
		// "Other ways to sign in" (lib/api/client.ts signInWithPassword): a
		// second phone session, used below where a call must come from the
		// phone's own sign-in (its push registration names it).
		{Operation: "POST /auth/desktop/login", Path: "/auth/desktop/login", Anonymous: true, Status: 200,
			Body:    `{"email":"alice@example.test","password":"` + contractPassword + `","device_label":"Alice's iPhone"}`,
			Capture: map[string]string{"phone_access": "access_token", "phone_refresh": "refresh_token", "phone_session": "session_id"}},
		// Every cold start spends one refresh (the access token is kept in
		// memory only); refresh tokens rotate.
		{Operation: "POST /auth/jwt/refresh", Path: "/auth/jwt/refresh", Anonymous: true, Status: 200,
			Body:    `{"refresh_token":"{{phone_refresh}}"}`,
			Capture: map[string]string{"phone_access": "access_token", "phone_refresh": "refresh_token"}},

		// The account (lib/api/endpoints.ts me, Profile, Change password,
		// Signed-in devices).
		{Operation: "GET /users/me", Path: "/users/me", Status: 200},
		{Operation: "PATCH /users/me", Path: "/users/me", Body: `{"display_name":"Alice"}`, Status: 200},
		{Operation: "PATCH /users/me", Path: "/users/me", Body: `{"display_name":null}`, Status: 200},
		{Operation: "GET /auth/password", Path: "/auth/password", Status: 200},
		{Operation: "GET /auth/devices", Path: "/auth/devices", Status: 200},

		// What every start sends in the background: the push registration,
		// which says which version and bundle the phone runs
		// (lib/push/register.ts, lib/appVersion): an iPhone's APNs token, the
		// app's version, its runtime, and the update's id (null for the bundle
		// built into the app). Then its removal at sign-out, the presence
		// beat, and a screen's crash report.
		{Operation: "POST /api/v1/push/devices", Path: "/api/v1/push/devices", Bearer: "{{phone_access}}", Status: 200,
			Body: `{"token":"0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0","kind":"apns","environment":"production","platform":"ios","label":"Alice's iPhone","language":"en","session_id":"{{phone_session}}","app_version":"1.1.0","update_id":"01a1e03b-db26-7530-aac0-81d0fffb29cc","runtime_version":"4a69ecbff714"}`},
		{Operation: "DELETE /api/v1/push/devices", Path: "/api/v1/push/devices?token=0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0", Bearer: "{{phone_access}}", Status: 204},
		{Operation: "GET /api/v1/push/preferences", Path: "/api/v1/push/preferences", Status: 200},
		{Operation: "PUT /api/v1/push/preferences", Path: "/api/v1/push/preferences", Body: `{"choices":{"output":false}}`, Status: 200},
		// lib/push/presence.ts: not in the builds' openapi.json; the answer
		// is not read (any 2xx).
		{Operation: "PUT /api/v1/presence", Path: "/api/v1/presence", Body: `{"client_id":"00112233445566778899aabbccddeeff","active":true}`, Status: 204},
		{Operation: "PUT /api/v1/presence", Path: "/api/v1/presence", Body: `{"client_id":"00112233445566778899aabbccddeeff","active":false}`, Status: 204},
		{Operation: "POST /api/v1/client-errors", Path: "/api/v1/client-errors", Status: 204,
			Body: `{"kind":"error-boundary","name":"TypeError","stack":"at InboxScreen (index.bundle:1:2)","path":"/(app)/inbox","build":"1.1.0 (4)"}`},
		{Operation: "GET /api/v1/support-contact", Path: "/api/v1/support-contact", Status: 200},

		// Settings: instructions, citations, API access.
		{Operation: "GET /api/v1/users/me/settings", Path: "/api/v1/users/me/settings", Status: 200},
		{Operation: "PATCH /api/v1/users/me/settings", Path: "/api/v1/users/me/settings", Body: `{"chat_instructions":"Answer in short paragraphs."}`, Status: 200},
		{Operation: "PATCH /api/v1/users/me/settings", Path: "/api/v1/users/me/settings", Body: `{"citations_enabled":false}`, Status: 200},
		{Operation: "PATCH /api/v1/users/me/settings", Path: "/api/v1/users/me/settings", Body: `{"api_access_enabled":true}`, Status: 200},
		// The instructions check asks the model too (use_model): with no
		// model proxy it answers the fixed patterns alone.
		{Operation: "POST /api/v1/system-instructions/lint", Path: "/api/v1/system-instructions/lint", Body: `{"text":"Always answer in French.","use_model":true}`, Status: 200},

		// Models (features/settings/models): each job's model; null is the
		// server's default. Each reads the model proxy's list of names, which
		// this test does not run, so all but the connections answer 503
		// here; their answers are held to the snapshot by the routes' own
		// tests (internal/chat, internal/modelchoices), not by this one.
		{Operation: "GET /api/v1/users/me/llm-setup-status", Path: "/api/v1/users/me/llm-setup-status", Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "GET /api/v1/global-model-connections", Path: "/api/v1/global-model-connections", Status: 200},
		{Operation: "GET /api/v1/model-connections", Path: "/api/v1/model-connections", Status: 200},
		{Operation: "GET /api/v1/users/me/chat-model", Path: "/api/v1/users/me/chat-model", Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "PUT /api/v1/users/me/chat-model", Path: "/api/v1/users/me/chat-model", Body: `{"model":null}`, Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "GET /api/v1/users/me/model-roles", Path: "/api/v1/users/me/model-roles", Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "PUT /api/v1/users/me/model-roles", Path: "/api/v1/users/me/model-roles", Body: `{"vision":null}`, Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "PUT /api/v1/users/me/model-roles", Path: "/api/v1/users/me/model-roles", Body: `{"image":null}`, Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "GET /api/v1/users/me/quick-model", Path: "/api/v1/users/me/quick-model", Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "PUT /api/v1/users/me/quick-model", Path: "/api/v1/users/me/quick-model", Body: `{"model":null}`, Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "GET /api/v1/users/me/studio-model", Path: "/api/v1/users/me/studio-model", Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},
		{Operation: "PUT /api/v1/users/me/studio-model", Path: "/api/v1/users/me/studio-model", Body: `{"model":null}`, Status: 503, Unavailable: accountModelProxy, Why: accountModelProxyWhy},

		// The activity log (Settings → Activity): the list with the screen's
		// limit and filters, the summary, a delete.
		{Operation: "GET /api/v1/logs", Path: "/api/v1/logs?limit=200", Status: 200},
		{Operation: "GET /api/v1/logs", Path: "/api/v1/logs?limit=200&level=ERROR&status=FAILED&source=document_processor", Status: 200},
		{Operation: "GET /api/v1/logs/summary", Path: "/api/v1/logs/summary?hours=24", Status: 200},
		{Operation: "DELETE /api/v1/logs/{log_id}", Path: "/api/v1/logs/2002", Status: 200},

		// API access (features/apiAccess): list, create, revoke.
		{Operation: "GET /api/v1/pats", Path: "/api/v1/pats", Status: 200},
		{Operation: "POST /api/v1/pats", Path: "/api/v1/pats", Body: `{"label":"My laptop","expires_in_days":30}`, Status: 200,
			Capture: map[string]string{"pat_id": "id"}},
		{Operation: "DELETE /api/v1/pats/{pat_id}", Path: "/api/v1/pats/{{pat_id}}", Status: 204},

		// Prompts (the chat's / panel and Settings → Prompts).
		{Operation: "GET /api/v1/prompts", Path: "/api/v1/prompts", Status: 200},
		{Operation: "POST /api/v1/prompts", Path: "/api/v1/prompts", Body: `{"name":"Summarise","prompt":"Summarise this in three lines.","mode":"transform"}`, Status: 200,
			Capture: map[string]string{"prompt_id": "id"}},
		{Operation: "PUT /api/v1/prompts/{prompt_id}", Path: "/api/v1/prompts/{{prompt_id}}", Body: `{"name":"Summarise briefly","prompt":"Summarise this in two lines.","mode":"transform"}`, Status: 200},
		{Operation: "DELETE /api/v1/prompts/{prompt_id}", Path: "/api/v1/prompts/{{prompt_id}}", Status: 200},

		// Memory (features/memory).
		{Operation: "GET /api/v1/users/me/memory", Path: "/api/v1/users/me/memory", Status: 200},
		{Operation: "PUT /api/v1/users/me/memory", Path: "/api/v1/users/me/memory", Body: `{"memory_md":"## Notes\n- Studies tides."}`, Status: 200},
		{Operation: "POST /api/v1/users/me/memory/reset", Path: "/api/v1/users/me/memory/reset", Body: `{}`, Status: 200},

		// The chat's + menu.
		{Operation: "GET /api/v1/agent/tools", Path: "/api/v1/agent/tools", Status: 200},

		// The Studio's language picker.
		// The languages come from the speech service's settings, which this
		// deployment does not configure (TTS_SERVICE).
		{Operation: "GET /api/v1/podcasts/languages", Path: "/api/v1/podcasts/languages", Status: 503, Unavailable: "the speech service (TTS_SERVICE is not configured here)",
			Why: "a server without speech has no languages; the Studio's picker shows the server's sentence"},
		// A Mermaid diagram in an answer, drawn by the server
		// (lib/render/mermaid/mermaidClient.ts; not in the builds'
		// openapi.json).
		{Operation: "POST /api/v1/diagrams/mermaid", Path: "/api/v1/diagrams/mermaid", Body: `{"code":"graph TD; A-->B","theme":"light"}`, Status: 404,
			Why: "retired: installedbuilds.Retired says what the build does with it"},

		// More → Admin (a superuser's screens).
		{Operation: "GET /api/v1/model-providers", Path: "/api/v1/model-providers", Status: 200},
		{Operation: "PUT /api/v1/model-connections/{connection_id}", Path: "/api/v1/model-connections/2010", Body: `{"enabled":false}`, Status: 200},
		{Operation: "DELETE /api/v1/model-connections/{connection_id}", Path: "/api/v1/model-connections/2010", Status: 200},

		// The browser hand-offs (features/settings/connectors/handoff.ts,
		// features/sources/drive/driveHandoff.ts): start, the page the
		// browser opens, and the completion. Google and Microsoft are not
		// configured in this deployment, so each start is the 503 "not set
		// up on this server", which the builds show as the server's sentence.
		{Operation: "POST /api/v1/app/handoff", Path: "/api/v1/app/handoff", Status: 503, Unavailable: "Google Drive (not configured here)", Why: accountHandoffWhy,
			Body: `{"purpose":"drive_connect","account_id":null,"code_challenge":"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}`},
		{Operation: "POST /api/v1/app/handoff", Path: "/api/v1/app/handoff", Status: 503, Unavailable: "Microsoft sign-in (not configured here)", Why: accountHandoffWhy,
			Body: `{"purpose":"microsoft_mail","account_id":null,"code_challenge":"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}`},
		{Operation: "POST /api/v1/app/handoff", Path: "/api/v1/app/handoff", Status: 503, Unavailable: "Google Drive (not configured here)", Why: accountHandoffWhy,
			Body: `{"purpose":"drive_picker","notebook_id":2070}`},
		// With no start, the page and the completion are sent with a code
		// and a completion the server does not hold: the browser sheet comes
		// back to the app as expired, and the app reads the completion's 410
		// as "expired" (confirmHandoff).
		{Operation: "GET /api/v1/app/handoff/{code}", Path: "/api/v1/app/handoff/contract-code-the-server-does-not-hold", Anonymous: true,
			Status: 303, Why: "a code that is not held (expired or spent) sends the browser sheet back to the app with status=expired, which the screen reports"},
		{Operation: "POST /api/v1/app/handoff/complete", Path: "/api/v1/app/handoff/complete",
			Status: 410, Why: "a completion the server no longer holds is 410, which confirmHandoff reads as \"expired\" and the screen asks to connect again",
			Body: `{"completion":"contract-completion-the-server-does-not-hold","code_verifier":"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"}`},

		// Single sign-on in the browser sheet (features/auth/sso.ts): the
		// start, the exchange that signs in, and the confirmation a
		// password change asks for. This deployment has single sign-on off
		// (it needs an identity provider), so each is the 404 that says so;
		// the builds offer single sign-on only when sign-in-options says it
		// is on, so they never send these to such a server.
		{Operation: "GET /api/v1/auth/oidc/app/start", Anonymous: true, Status: 404, Why: "single sign-on is off in this deployment; the builds offer it only when sign-in-options says it is on",
			Path: "/api/v1/auth/oidc/app/start?app_redirect=soloclef%3A%2F%2Fauth&challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&challenge_method=S256&device=Alice%27s+iPhone&choose_account=1"},
		{Operation: "POST /api/v1/auth/oidc/app/exchange", Path: "/api/v1/auth/oidc/app/exchange", Anonymous: true, Status: 404, Why: "single sign-on is off in this deployment; the builds offer it only when sign-in-options says it is on",
			Body: `{"code":"contract-code","verifier":"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk","device_label":"Alice's iPhone"}`},
		{Operation: "POST /api/v1/auth/oidc/app/confirm", Path: "/api/v1/auth/oidc/app/confirm", Status: 404, Why: "single sign-on is off in this deployment; the builds offer it only when sign-in-options says it is on",
			Body: `{"code":"contract-code","verifier":"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"}`},
	},
}
