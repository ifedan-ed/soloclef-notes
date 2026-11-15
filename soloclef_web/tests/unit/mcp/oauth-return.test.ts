import assert from "node:assert/strict";
import test from "node:test";
import { oauthReadiness } from "@/features/mcp-server-form/model/oauth-readiness";
import {
	oauthReturnPath,
	readOAuthReturn,
	withoutOAuthMarkers,
} from "@/features/mcp-server-form/model/oauth-return";

test("the return markers say how the sign-in went and which server it was", () => {
	const back = readOAuthReturn(new URLSearchParams("mcp_oauth=connected&mcp_connector=42"));
	assert.equal(back?.outcome, "connected");
	assert.equal(back?.tone, "success");
	assert.equal(back?.connectorId, 42);
	for (const outcome of ["denied", "failed", "expired"]) {
		assert.equal(readOAuthReturn(new URLSearchParams(`mcp_oauth=${outcome}`))?.tone, "error");
	}
	assert.equal(readOAuthReturn(new URLSearchParams("tab=all")), null);
});

test("an unknown outcome is said as a failure, and a malformed id names no server", () => {
	const odd = readOAuthReturn(new URLSearchParams("mcp_oauth=weird&mcp_connector=4x"));
	assert.equal(odd?.outcome, "failed");
	assert.equal(odd?.connectorId, null);
});

test("the markers are dropped and the rest of the query kept", () => {
	assert.equal(
		withoutOAuthMarkers(new URLSearchParams("a=1&mcp_oauth=connected&mcp_connector=2")),
		"?a=1"
	);
	assert.equal(withoutOAuthMarkers(new URLSearchParams("mcp_oauth=denied")), "");
});

test("the return path is this site's page, never another site, without old markers", () => {
	assert.equal(
		oauthReturnPath("/dashboard/settings/connectors", "?x=1&mcp_oauth=failed"),
		"/dashboard/settings/connectors?x=1"
	);
	assert.equal(oauthReturnPath("//evil.example/x", ""), "/");
});

test("discovery's answer decides the client id field and what blocks saving", () => {
	assert.equal(oauthReadiness(undefined, true).problem !== null, true);
	assert.deepEqual(oauthReadiness(undefined, false), {
		needsClientId: false,
		problem: null,
		note: null,
	});
	const dynamic = oauthReadiness(
		{
			status: "success",
			authorization_server: "https://auth.example.com/",
			dynamic_registration: true,
		},
		false
	);
	assert.equal(dynamic.needsClientId, false);
	assert.match(dynamic.note ?? "", /auth\.example\.com.*registers itself/);
	const manual = oauthReadiness(
		{
			status: "success",
			authorization_server: "https://auth.example.com",
			dynamic_registration: false,
		},
		false
	);
	assert.equal(manual.needsClientId, true);
	const refused = oauthReadiness({ status: "error", message: "No OAuth here" }, false);
	assert.equal(refused.problem, "No OAuth here");
});
