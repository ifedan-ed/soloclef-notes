import assert from "node:assert/strict";
import test from "node:test";

import {
	buildClientErrorPayload,
	installGlobalClientErrorHandlers,
	reportClientError,
	resetClientErrorReports,
} from "@/lib/client-error-report";

// Run with: pnpm exec tsx --test tests/unit/client-error-report.test.ts

type Sent = { url: string; init: RequestInit };

function withBrowser(pathname: string, run: (sent: Sent[]) => void) {
	const sent: Sent[] = [];
	const g = globalThis as Record<string, unknown>;
	const prevWindow = g.window;
	const prevFetch = g.fetch;
	g.window = { location: { pathname } };
	g.fetch = (url: string, init: RequestInit) => {
		sent.push({ url, init });
		return Promise.resolve(new Response(null, { status: 204 }));
	};
	resetClientErrorReports();
	try {
		run(sent);
	} finally {
		g.window = prevWindow;
		g.fetch = prevFetch;
		resetClientErrorReports();
	}
}

test("payload keeps the pathname only and caps sizes", () => {
	const err = Object.assign(new TypeError("x".repeat(5000)), { digest: "d1", requestId: "req_1" });
	const p = buildClientErrorPayload(
		"error-boundary",
		err,
		"/dashboard/1/notebooks/3?token=secret#frag"
	);
	assert.equal(p.path, "/dashboard/1/notebooks/3");
	assert.equal(p.name, "TypeError");
	assert.equal(p.message.length, 1000);
	assert.ok(p.stack.length <= 6000);
	assert.equal(p.digest, "d1");
	assert.equal(p.request_id, "req_1");
	assert.equal(p.kind, "error-boundary");
	assert.ok(p.build.length > 0 && p.build.length <= 64);
});

test("non-Error rejections still produce a message", () => {
	assert.equal(
		buildClientErrorPayload("unhandled-rejection", "plain string", "/").message,
		"plain string"
	);
	assert.equal(buildClientErrorPayload("unhandled-rejection", undefined, "/").message, "");
});

test("reports once per distinct error and posts JSON with credentials", () => {
	withBrowser("/dashboard/1", (sent) => {
		const err = new Error("boom");
		assert.ok(reportClientError("window-error", err));
		assert.equal(reportClientError("window-error", err), null);
		assert.equal(sent.length, 1);
		assert.ok(sent[0].url.endsWith("/api/v1/client-errors"));
		assert.equal(sent[0].init.method, "POST");
		assert.equal(sent[0].init.credentials, "include");
		const body = JSON.parse(String(sent[0].init.body));
		assert.equal(body.message, "boom");
		assert.equal(body.path, "/dashboard/1");
	});
});

test("stops after a handful of reports per page", () => {
	withBrowser("/x", (sent) => {
		for (let i = 0; i < 20; i++) reportClientError("window-error", new Error(`e${i}`));
		assert.equal(sent.length, 5);
	});
});

test("never throws, even when fetch does", () => {
	const g = globalThis as Record<string, unknown>;
	const prevWindow = g.window;
	const prevFetch = g.fetch;
	g.window = { location: { pathname: "/" } };
	g.fetch = () => {
		throw new Error("network down");
	};
	resetClientErrorReports();
	try {
		assert.equal(reportClientError("global-error", new Error("x")), null);
	} finally {
		g.window = prevWindow;
		g.fetch = prevFetch;
	}
});

test("a report says whether the page reloaded itself to recover", () => {
	assert.equal(
		buildClientErrorPayload("error-boundary", new Error("x"), "/").recovered_by_reload,
		false
	);
	withBrowser("/dashboard/notebooks/3", (sent) => {
		const err = Object.assign(new Error("Failed to load chunk a.js from module 1"), {
			name: "ChunkLoadError",
		});
		assert.ok(reportClientError("error-boundary", err, { recoveredByReload: true }));
		assert.equal(JSON.parse(String(sent[0].init.body)).recovered_by_reload, true);
		assert.equal(sent[0].init.keepalive, true, "sent even as the page reloads");
	});
});

test("the global handlers let the recovery take an error before reporting it", () => {
	const listeners = new Map<string, (event: unknown) => void>();
	const g = globalThis as Record<string, unknown>;
	const prevWindow = g.window;
	const prevFetch = g.fetch;
	const sent: string[] = [];
	g.window = {
		location: { pathname: "/dashboard" },
		addEventListener: (type: string, listener: (event: unknown) => void) => {
			listeners.set(type, listener);
		},
	};
	g.fetch = (_url: string, init: RequestInit) => {
		sent.push(String(init.body));
		return Promise.resolve(new Response(null, { status: 204 }));
	};
	resetClientErrorReports();
	const taken = new Set<unknown>();
	const handled = new Error("handled by a reload");
	try {
		installGlobalClientErrorHandlers((_kind, error) => {
			taken.add(error);
			return error === handled;
		});
		const rejected = listeners.get("unhandledrejection");
		assert.ok(rejected && listeners.get("error"));
		rejected({ reason: handled });
		assert.equal(sent.length, 0, "a recovered error reports itself, not twice");
		const other = new Error("not a chunk");
		rejected({ reason: other });
		listeners.get("error")?.({ error: new Error("thrown") });
		assert.equal(sent.length, 2);
		assert.ok(taken.has(handled) && taken.has(other));
	} finally {
		g.window = prevWindow;
		g.fetch = prevFetch;
		resetClientErrorReports();
	}
});
