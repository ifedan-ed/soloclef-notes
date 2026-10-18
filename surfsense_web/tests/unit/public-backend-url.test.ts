import assert from "node:assert/strict";
import test from "node:test";

import { publicBackendBaseUrl } from "@/lib/env-config";

// Run with: pnpm exec tsx --test tests/unit/public-backend-url.test.ts

test("proxy mode hands an outside client this page's own origin", () => {
	// The agent-setup dialog used to fall back to http://localhost:8000, which
	// is the backend container's address, not anything the user's laptop can reach.
	assert.equal(publicBackendBaseUrl("", "https://notes.example.com"), "https://notes.example.com");
});

test("an explicitly configured backend URL wins", () => {
	assert.equal(
		publicBackendBaseUrl("https://api.example.test", "https://notes.example.com"),
		"https://api.example.test"
	);
});

test("with neither, the caller's own placeholder is used", () => {
	assert.equal(publicBackendBaseUrl("", undefined), undefined);
});
