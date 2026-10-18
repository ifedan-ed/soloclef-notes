import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

// Run with: pnpm exec tsx --test tests/unit/self-hosted-defaults.test.ts

/**
 * Everything here ships in every server's website, so no one server's host,
 * mail domain or sign-in name may be written into it: those come from the
 * server's own settings (lib/brand, lib/sso-settings, the billing admin page).
 * Tests and the Playwright config are not shipped and are not scanned.
 */
const WEB = path.resolve(__dirname, "../..");
const SHIPPED_DIRS = [
	"app",
	"atoms",
	"components",
	"content",
	"contexts",
	"contracts",
	"features",
	"hooks",
	"i18n",
	"lib",
	"messages",
	"public",
	"types",
	"zero",
];
const SHIPPED_FILES = [
	"Dockerfile",
	"instrumentation.ts",
	"instrumentation-client.ts",
	"mdx-components.tsx",
	"next.config.ts",
	"proxy.ts",
	"source.config.ts",
];
const TEXT = /\.(ts|tsx|js|mjs|cjs|json|md|mdx|css|svg|txt|xml|webmanifest|ya?ml|html)$/;
const ONE_SERVER = /example|soloclef\.com/i;

function walk(dir: string): string[] {
	return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
		const full = path.join(dir, entry.name);
		if (entry.isDirectory()) return walk(full);
		return TEXT.test(entry.name) ? [full] : [];
	});
}

test("no shipped file names one server's address, mail or sign-in", () => {
	const files = [
		...SHIPPED_DIRS.flatMap((dir) => walk(path.join(WEB, dir))),
		...SHIPPED_FILES.map((file) => path.join(WEB, file)),
	];
	assert.ok(files.length > 500, "the scan found the source tree");
	const named = files.filter((file) => ONE_SERVER.test(readFileSync(file, "utf8")));
	assert.deepEqual(
		named.map((file) => path.relative(WEB, file)),
		[]
	);
});
