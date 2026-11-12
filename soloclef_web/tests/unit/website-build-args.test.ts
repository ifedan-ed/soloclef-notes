import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

// Run with: pnpm exec tsx --test tests/unit/website-build-args.test.ts

/**
 * The website is built with this server's own settings (Dockerfile, builder
 * stage) and docker-compose.go.yml hands them over from docker/.env.go. A
 * build arg the compose file passes but the Dockerfile does not declare and
 * export never reaches `next build`; a setting the website reads that compose
 * never passes is always empty there, while the API reads its own setting, so
 * the two disagree. NEXT_PUBLIC_SIGNUPS_OPEN was that once: the API's
 * sign-in options (which the phone app follows) reported sign-ups open while
 * the website was always built with them closed.
 */
const WEB = path.resolve(__dirname, "../..");
const dockerfile = readFileSync(path.join(WEB, "Dockerfile"), "utf8");
const compose = readFileSync(path.join(WEB, "../docker/docker-compose.go.yml"), "utf8");

/** The frontend service's build args in docker-compose.go.yml, name -> value. */
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

test("the website offers sign-up from the same setting the API reads", () => {
	// lib/signups.ts reads it at build time; the API reads SOLOCLEF_API_SIGNUP_ALLOWED at start.
	assert.equal(
		composeBuildArgs().get("NEXT_PUBLIC_SIGNUPS_OPEN"),
		// biome-ignore lint/suspicious/noTemplateCurlyInString: compose's interpolation, read as text
		"${SOLOCLEF_API_SIGNUP_ALLOWED:?}"
	);
	// The API is given the same setting, so the two cannot disagree.
	assert.match(compose, /^ {6}SOLOCLEF_API_SIGNUP_ALLOWED: \$\{SOLOCLEF_API_SIGNUP_ALLOWED:\?\}$/m);
	assert.match(
		readFileSync(path.join(WEB, "lib/signups.ts"), "utf8"),
		/process\.env\.NEXT_PUBLIC_SIGNUPS_OPEN === "true"/
	);
});
