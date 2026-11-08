import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

// Run with: pnpm exec tsx --test tests/unit/website-build-args.test.ts

/**
 * The website is built with this server's own settings (Dockerfile, builder
 * stage) and docker-compose.yml hands them over from docker/.env. A build arg
 * the compose file passes but the Dockerfile does not declare and export never
 * reaches `next build`; a setting the website reads that compose never passes
 * is always empty there, while the backend reads docker/.env itself, so the
 * two disagree. NEXT_PUBLIC_SIGNUPS_OPEN was that: the backend's
 * /api/v1/sign-in-options (which the phone app follows) reported the line
 * while the website was always built with sign-ups closed.
 */
const WEB = path.resolve(__dirname, "../..");
const dockerfile = readFileSync(path.join(WEB, "Dockerfile"), "utf8");
const compose = readFileSync(path.join(WEB, "../docker/docker-compose.yml"), "utf8");

/** The frontend service's build args in docker-compose.yml, name -> value. */
function composeBuildArgs(): Map<string, string> {
	const service = compose.split(/^ {2}frontend:\n/m)[1] ?? "";
	const block = service.split(/^ {6}args:\n/m)[1] ?? "";
	const args = new Map<string, string>();
	for (const line of block.split("\n")) {
		if (/^\s*#/.test(line)) continue;
		const match = /^ {8}([A-Z0-9_]+): (.*)$/.exec(line);
		if (!match) break;
		args.set(match[1], match[2].trim());
	}
	return args;
}

test("every build arg compose passes reaches next build", () => {
	const args = composeBuildArgs();
	assert.ok(args.has("NEXT_PUBLIC_SITE_URL"), "the compose file's frontend build args were found");
	for (const name of args.keys()) {
		assert.match(dockerfile, new RegExp(`^ARG ${name}=`, "m"), `${name} is declared`);
		assert.match(dockerfile, new RegExp(`\\b${name}="\\$\\{${name}\\}"`), `${name} is exported`);
	}
});

test("the website offers sign-up from the same docker/.env line the backend reads", () => {
	// lib/signups.ts reads it at build time; app/sign_in_options reads it at start.
	// biome-ignore lint/suspicious/noTemplateCurlyInString: compose's interpolation, read as text
	assert.equal(composeBuildArgs().get("NEXT_PUBLIC_SIGNUPS_OPEN"), "${NEXT_PUBLIC_SIGNUPS_OPEN:-}");
	assert.match(
		readFileSync(path.join(WEB, "lib/signups.ts"), "utf8"),
		/process\.env\.NEXT_PUBLIC_SIGNUPS_OPEN === "true"/
	);
});
