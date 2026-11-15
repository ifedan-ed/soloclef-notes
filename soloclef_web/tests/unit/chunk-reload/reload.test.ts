import assert from "node:assert/strict";
import test from "node:test";

import {
	BUILD_ID,
	type ReloadDeps,
	recoverByReload,
	willRecoverByReload,
} from "@/lib/chunk-reload/recover";
import { claimReload, mayReload, RELOAD_GUARD_MS } from "@/lib/chunk-reload/reload-guard";

// Run with: pnpm exec tsx --test tests/unit/chunk-reload/reload.test.ts

/** sessionStorage as a tab has it: survives the reload, and is all the guard keeps. */
class TabStore implements Storage {
	private items = new Map<string, string>();
	get length() {
		return this.items.size;
	}
	clear() {
		this.items.clear();
	}
	getItem(key: string) {
		return this.items.get(key) ?? null;
	}
	key(index: number) {
		return [...this.items.keys()][index] ?? null;
	}
	removeItem(key: string) {
		this.items.delete(key);
	}
	setItem(key: string, value: string) {
		this.items.set(key, value);
	}
}

/** A store the browser refuses to use, as some private modes do. */
const refusing = {
	getItem(): string | null {
		throw new Error("SecurityError");
	},
	setItem() {
		throw new Error("QuotaExceededError");
	},
};

function chunkError(): Error {
	const error = new Error(
		"Failed to load chunk /_next/static/chunks/0lrjxh23knt79.js from module 7"
	);
	error.name = "ChunkLoadError";
	return error;
}

test("one reload per build: the first is taken, a second soon after is refused", () => {
	const store = new TabStore();
	const t0 = 1_700_000_000_000;
	assert.equal(mayReload(store, "build-a", t0), true);
	assert.equal(claimReload(store, "build-a", t0), true);
	// The page failed again after its reload: the error page, not another reload.
	assert.equal(mayReload(store, "build-a", t0 + 5_000), false);
	assert.equal(claimReload(store, "build-a", t0 + 5_000), false);
	assert.equal(claimReload(store, "build-a", t0 + RELOAD_GUARD_MS - 1), false);
});

test("a new build, or the same one long after, gets its own reload", () => {
	const store = new TabStore();
	const t0 = 1_700_000_000_000;
	assert.equal(claimReload(store, "build-a", t0), true);
	// The reload landed on a new build, and that one's deploy is followed by another.
	assert.equal(claimReload(store, "build-b", t0 + 1_000), true);
	// A tab left open for hours meets an unrelated failure of the same build.
	assert.equal(claimReload(store, "build-a", t0 + RELOAD_GUARD_MS), true);
});

test("no usable storage means no reload, since a loop could not be told apart", () => {
	assert.equal(mayReload(null, "build-a", 1), false);
	assert.equal(claimReload(null, "build-a", 1), false);
	assert.equal(mayReload(refusing, "build-a", 1), false);
	assert.equal(
		claimReload({ getItem: () => null, setItem: refusing.setItem }, "build-a", 1),
		false
	);
});

test("an unreadable stored value is not a recent reload", () => {
	const store = new TabStore();
	store.setItem(`soloclef:chunk-reload:${BUILD_ID}`, "garbage");
	assert.equal(mayReload(store, BUILD_ID, 1_700_000_000_000), true);
});

function deps(store: Storage | null, now: number) {
	const calls = { reloads: 0, reports: [] as unknown[][] };
	const d: ReloadDeps = {
		store,
		now,
		reload: () => {
			calls.reloads += 1;
		},
		report: ((...args: unknown[]) => {
			calls.reports.push(args);
			return null;
		}) as ReloadDeps["report"],
	};
	return { d, calls };
}

test("a missing chunk reloads the page once and is reported as recovered", () => {
	const store = new TabStore();
	const error = chunkError();
	const first = deps(store, 1_700_000_000_000);
	assert.equal(willRecoverByReload(error, first.d), true);
	assert.equal(recoverByReload("error-boundary", error, first.d), true);
	assert.equal(first.calls.reloads, 1);
	assert.deepEqual(first.calls.reports, [["error-boundary", error, { recoveredByReload: true }]]);

	// After the reload the same build fails again: the caller shows the error.
	const again = deps(store, 1_700_000_003_000);
	assert.equal(willRecoverByReload(error, again.d), false);
	assert.equal(recoverByReload("error-boundary", error, again.d), false);
	assert.equal(again.calls.reloads, 0);
	assert.equal(again.calls.reports.length, 0, "the caller reports it, as any error");
});

test("any other error is left to the caller untouched", () => {
	const store = new TabStore();
	const { d, calls } = deps(store, 1_700_000_000_000);
	const error = new TypeError("x is undefined");
	assert.equal(willRecoverByReload(error, d), false);
	assert.equal(recoverByReload("unhandled-rejection", error, d), false);
	assert.equal(calls.reloads, 0);
	assert.equal(calls.reports.length, 0);
	assert.equal(store.length, 0, "and it does not use up the build's reload");
});

test("without storage a missing chunk is the error page's, not a reload", () => {
	const { d, calls } = deps(null, 1_700_000_000_000);
	assert.equal(willRecoverByReload(chunkError(), d), false);
	assert.equal(recoverByReload("error-boundary", chunkError(), d), false);
	assert.equal(calls.reloads, 0);
});
