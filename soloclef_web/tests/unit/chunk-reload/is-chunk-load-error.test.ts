import assert from "node:assert/strict";
import test from "node:test";

import { isChunkLoadError } from "@/lib/chunk-reload/is-chunk-load-error";

// Run with: pnpm exec tsx --test tests/unit/chunk-reload/is-chunk-load-error.test.ts

function named(name: string, message: string): Error {
	const error = new Error(message);
	error.name = name;
	return error;
}

test("the failures a missing chunk causes, as each bundler and browser words them", () => {
	for (const error of [
		// What the live site logged after a deploy (Turbopack's runtime).
		named(
			"ChunkLoadError",
			"Failed to load chunk /_next/static/chunks/0lrjxh23knt79.js from module 41234"
		),
		named("ChunkLoadError", "anything at all"),
		new Error("Failed to load chunk /_next/static/chunks/a.css from runtime for chunk b.js"),
		new Error(
			"Failed to load chunk /_next/static/chunks/c.js as a runtime dependency of chunk /_next/static/chunks/d.js"
		),
		new Error("Failed to load chunk /_next/static/chunks/e.js from an HMR update"),
		// Turbopack adds the fetch's own error when there is one.
		new Error(
			"Failed to load chunk /_next/static/chunks/f.js from module 7: TypeError: Failed to fetch"
		),
		new Error("Loading chunk 712 failed.\n(error: https://x/_next/static/chunks/712.js)"),
		new Error("Loading chunk 712 failed."),
		new Error("Loading CSS chunk 9 failed."),
		new Error("Loading CSS chunk 9 failed.\n(https://x/_next/static/css/9.css)"),
		new TypeError("Failed to fetch dynamically imported module: https://x/_next/static/a.js"),
		new TypeError("error loading dynamically imported module: https://x/a.js"),
		new TypeError("error loading dynamically imported module"),
		new TypeError("Importing a module script failed."),
	]) {
		assert.equal(isChunkLoadError(error), true, `${error.name}: ${error.message}`);
	}
});

test("one level of cause is looked into, as when something awaited the import", () => {
	const wrapped = new Error("could not open the player", {
		cause: named("ChunkLoadError", "Failed to load chunk x.js from module 1"),
	});
	assert.equal(isChunkLoadError(wrapped), true);
});

test("every other error is the error page's", () => {
	for (const error of [
		new TypeError("Cannot read properties of undefined (reading 'id')"),
		new Error("Failed to fetch"),
		new Error("Loading notebook failed"),
		named("NetworkError", "chunk"),
		// The words of a real failure inside another message are not one:
		// quoted by a server's answer, prefixed or followed by other text.
		new Error("The server said: Failed to load chunk x.js from module 1"),
		new Error("Failed to load chunk list for this notebook"),
		new Error("Failed to load chunk x.js from the archive"),
		new Error("Could not save: Loading chunk 3 failed."),
		new Error("Loading chunk 3 failed. Try again later"),
		new Error("Loading chunk 3 failed.\n(x)\nand more"),
		new TypeError("Upload failed: Failed to fetch dynamically imported module: x.js"),
		new TypeError("Failed to fetch dynamically imported module"),
		new TypeError("error loading dynamically imported modules for the editor"),
		new TypeError("Importing a module script failed. Please reload"),
		"Loading chunk 1 failed.",
		undefined,
		null,
		{ message: 42 },
		new Error("outer", { cause: new Error("inner", { cause: named("ChunkLoadError", "x") }) }),
	]) {
		assert.equal(isChunkLoadError(error), false, String(error));
	}
});
