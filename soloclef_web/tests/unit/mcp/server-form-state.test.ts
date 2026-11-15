import assert from "node:assert/strict";
import test from "node:test";
import {
	emptyServerForm,
	hasErrors,
	type ServerFormState,
	serverConfigFromForm,
	serverFormFromAnswer,
	serverUrlProblem,
	validateServerForm,
} from "@/features/mcp-server-form/model/server-form-state";

function form(part: Partial<ServerFormState>): ServerFormState {
	return { ...emptyServerForm(), name: "Linear", url: "https://mcp.linear.app/mcp", ...part };
}

const noOAuth = { needsClientId: false };

test("a filled-in server with no sign-in has nothing to say", () => {
	assert.deepEqual(validateServerForm(form({}), noOAuth), {});
});

test("the address must be a full https URL without credentials in it", () => {
	assert.match(serverUrlProblem("") ?? "", /Enter/);
	assert.match(serverUrlProblem("mcp.example.com") ?? "", /full URL/);
	assert.match(serverUrlProblem("http://mcp.example.com/mcp") ?? "", /https/);
	assert.match(serverUrlProblem("https://me:pw@mcp.example.com/mcp") ?? "", /header/);
	assert.equal(serverUrlProblem("  https://mcp.example.com/mcp  "), null);
});

test("each sign-in asks for its own fields, and only those", () => {
	const header = validateServerForm(
		form({ auth: "header", headerName: "", headerValue: "" }),
		noOAuth
	);
	assert.ok(header.headerName && header.headerValue);
	assert.match(
		validateServerForm(form({ auth: "header", headerName: "X API Key", headerValue: "k" }), noOAuth)
			.headerName ?? "",
		/letters, digits/
	);
	assert.ok(validateServerForm(form({ auth: "bearer", bearerToken: "  " }), noOAuth).bearerToken);
	// A bearer token typed while "None" is chosen is not asked about.
	assert.deepEqual(validateServerForm(form({ auth: "none", bearerToken: "" }), noOAuth), {});
});

test("OAuth asks for a client id only when the server cannot register the app", () => {
	const oauth = form({ auth: "oauth" });
	assert.deepEqual(validateServerForm(oauth, noOAuth), {});
	assert.ok(validateServerForm(oauth, { needsClientId: true }).clientId);
	assert.deepEqual(validateServerForm({ ...oauth, clientId: "abc" }, { needsClientId: true }), {});
	// Discovery's refusal blocks saving where no field can fix it.
	assert.equal(
		validateServerForm(oauth, { needsClientId: false, oauthProblem: "No sign-in" }).oauth,
		"No sign-in"
	);
});

test("extra headers cannot repeat each other or the sign-in's own header", () => {
	const rows = (names: string[]) => names.map((name, i) => ({ id: `r${i}`, name, value: "v" }));
	const duplicate = validateServerForm(form({ extraHeaders: rows(["X-Org", "x-org"]) }), noOAuth);
	assert.match(duplicate["row:r1:name"] ?? "", /already listed/);

	const bearer = validateServerForm(
		form({ auth: "bearer", bearerToken: "t", extraHeaders: rows(["Authorization"]) }),
		noOAuth
	);
	assert.match(bearer["row:r0:name"] ?? "", /Authorization/);

	const keyHeader = validateServerForm(
		form({
			auth: "header",
			headerName: "X-API-Key",
			headerValue: "k",
			extraHeaders: rows(["x-api-key"]),
		}),
		noOAuth
	);
	assert.match(keyHeader["row:r0:name"] ?? "", /API key already/);

	// A row left wholly blank is an unfilled row, and a half-filled one is not.
	assert.equal(
		hasErrors(
			validateServerForm(form({ extraHeaders: [{ id: "b", name: "", value: "" }] }), noOAuth)
		),
		false
	);
	const half = validateServerForm(
		form({ extraHeaders: [{ id: "h", name: "X-A", value: "" }] }),
		noOAuth
	);
	assert.ok(half["row:h:value"]);
});

test("a name is required for saving and held to the column's width", () => {
	assert.ok(validateServerForm(form({ name: "  " }), noOAuth).name);
	assert.ok(validateServerForm(form({ name: "n".repeat(101) }), noOAuth).name);
});

test("the body carries the chosen sign-in only, trimmed, with blank rows dropped", () => {
	const state = form({
		url: " https://mcp.example.com/mcp ",
		transport: "sse",
		auth: "header",
		headerName: " X-API-Key ",
		headerValue: "secret",
		bearerToken: "left over from another choice",
		extraHeaders: [
			{ id: "a", name: " X-Org ", value: "acme" },
			{ id: "b", name: "", value: "" },
		],
	});
	assert.deepEqual(serverConfigFromForm(state), {
		url: "https://mcp.example.com/mcp",
		transport: "sse",
		auth: { type: "header", header_name: "X-API-Key", value: "secret" },
		headers: { "X-Org": "acme" },
	});
	assert.deepEqual(serverConfigFromForm(form({ auth: "oauth" })).auth, { type: "oauth" });
	assert.deepEqual(
		serverConfigFromForm(form({ auth: "oauth", clientId: " cid ", clientSecret: "s" })).auth,
		{ type: "oauth", client_id: "cid", client_secret: "s" }
	);
});

test("a saved server's masked answer fills the form, and saved unchanged sends the masks back", () => {
	const saved = serverFormFromAnswer("Search", {
		url: "https://mcp.example.com/mcp?api_key=••••abcd",
		transport: "streamable-http",
		auth: { type: "header", header_name: "X-API-Key", value: "••••wxyz" },
		headers: { "X-Org": "••••" },
	});
	assert.ok(saved);
	assert.equal(saved.oauth, null);
	assert.equal(saved.state.headerValue, "••••wxyz");
	const body = serverConfigFromForm(saved.state);
	assert.deepEqual(body.auth, { type: "header", header_name: "X-API-Key", value: "••••wxyz" });
	assert.deepEqual(body.headers, { "X-Org": "••••" });
	assert.equal(body.url, "https://mcp.example.com/mcp?api_key=••••abcd");
});

test("a saved OAuth server says whether it is signed in", () => {
	const saved = serverFormFromAnswer("Linear", {
		url: "https://mcp.linear.app/mcp",
		transport: "streamable-http",
		auth: {
			type: "oauth",
			client_id: null,
			client_secret: null,
			dynamic_registration: true,
			authorization_server: "https://linear.app",
			authorized: true,
		},
		headers: {},
	});
	assert.deepEqual(saved?.oauth, {
		authorized: true,
		dynamicRegistration: true,
		authorizationServer: "https://linear.app",
	});
	assert.equal(saved?.state.clientId, "");
});

test("an answer in another shape is not guessed into a form", () => {
	assert.equal(
		serverFormFromAnswer("Old", {
			url: "https://x.example",
			headers: { a: "b" },
			transport: "http",
		}),
		null
	);
	assert.equal(serverFormFromAnswer("None", undefined), null);
});
