import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import { buildReportMailto } from "@/lib/brand/report";
import {
	currentSupportEmail,
	loadSupportEmail,
	parseSupportEmail,
	resetSupportEmailForTests,
} from "@/lib/brand/support-email";

afterEach(() => resetSupportEmailForTests());

const reply = (body: unknown, ok = true) =>
	(async () => ({ ok, json: async () => body })) as unknown as typeof fetch;

test("only one plain address is accepted from the endpoint", () => {
	assert.equal(parseSupportEmail({ email: " help@example.org " }), "help@example.org");
	for (const bad of [
		null,
		{},
		{ email: 7 },
		{ email: "" },
		{ email: "a@b" },
		{ email: "a@b.c?cc=x@y.z" },
		{ email: "a@b.c, d@e.f" },
	]) {
		assert.equal(parseSupportEmail(bad), null, JSON.stringify(bad));
	}
});

test("there is no address until the server's loads (none is built in)", async () => {
	assert.equal(currentSupportEmail(), "");
	assert.match(buildReportMailto(["x"]), /^mailto:\?/);
	assert.equal(currentSupportEmail("build@example.org"), "build@example.org");
	assert.equal(await loadSupportEmail(reply({ email: "help@example.org" })), "help@example.org");
	assert.equal(currentSupportEmail(), "help@example.org");
	assert.match(buildReportMailto(["x"]), /^mailto:help@example\.org\?/);
});

test("a failed or odd reply falls back to the build's address and retries later", async () => {
	const built = "build@example.org";
	assert.equal(await loadSupportEmail(reply({}, false), built), built);
	assert.equal(await loadSupportEmail(reply({ email: "nope" }), built), built);
	assert.equal(await loadSupportEmail(reply({ email: "" }), built), built, "none published");
	const failing = (async () => {
		throw new Error("offline");
	}) as unknown as typeof fetch;
	assert.equal(await loadSupportEmail(failing, built), built);
	assert.equal(await loadSupportEmail(failing), "", "no build address either");
	assert.equal(await loadSupportEmail(reply({ email: "ok@example.org" })), "ok@example.org");
});
