import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import { ROUTES_CADDY } from "../caddy-routes";

// Run with: pnpm exec tsx --test tests/unit/static-site/kept-assets-paths.test.ts

/**
 * Earlier builds' hashed files are kept in a volume (site-image/kept-assets.sh)
 * that four files name: the script that fills it, the image that runs the
 * script, the compose file that mounts it, and the Caddy routes that serve
 * from it. Each place disagreeing fails quietly (old tabs reload instead of
 * loading their code), so the agreement is checked here.
 */
const WEB = path.resolve(__dirname, "../../..");
const script = readFileSync(path.join(WEB, "site-image/kept-assets.sh"), "utf8");
const dockerfile = readFileSync(path.join(WEB, "Dockerfile"), "utf8");
const compose = readFileSync(path.join(WEB, "../docker/docker-compose.go.yml"), "utf8");

const kept = /^KEPT=(\S+)$/m.exec(script)?.[1];

test("the script, the volume mount and Caddy name the same place", () => {
	assert.ok(kept, "kept-assets.sh names its directory");
	assert.match(compose, new RegExp(`^ {6}- site_assets:${kept}$`, "m"), "the proxy mounts it");
	assert.match(compose, /^ {2}site_assets:$/m, "the volume is declared");
	assert.match(ROUTES_CADDY, new RegExp(`^\\s*root @keptAsset ${kept}/files$`, "m"));
});

test("only hashed files are looked for in the volume, and only when this build lacks them", () => {
	const block = /@keptAsset \{([\s\S]*?)\n\t\}/.exec(ROUTES_CADDY)?.[1] ?? "";
	assert.match(block, /^\s*path \/_next\/static\/\*$/m);
	assert.match(block, /not file \{\s*root \/srv\s*try_files \{path\}\s*\}/);
	// Caddy applies only the first `root` that matches, so the general one
	// must come after it or the volume is never used.
	const directive = (text: string) => ROUTES_CADDY.search(new RegExp(`^\\t${text}$`, "m"));
	assert.ok(directive("root @keptAsset \\S+") >= 0);
	assert.ok(directive("root @keptAsset \\S+") < directive("root \\* /srv"));
});

test("the image keeps its files and prunes with a 14-day window at every start", () => {
	assert.match(dockerfile, /^COPY site-image\/kept-assets\.sh \/usr\/local\/bin\/kept-assets$/m);
	assert.match(dockerfile, /kept-assets keep && kept-assets prune 14;.*exec caddy run/);
});
