#!/usr/bin/env node
/**
 * Snapshot the parts of the backend's OpenAPI document the app depends on.
 *
 * The contract test (src/lib/api/__tests__/contract.test.ts) checks the app's
 * zod schemas and routes against this snapshot, so a backend rename fails the
 * mobile test suite instead of failing silently on a phone. Refresh it from
 * any running backend of the version the app targets:
 *
 *   node scripts/fetch-openapi.mjs http://127.0.0.1:8000
 *
 * (The hermetic e2e stack, docker/docker-compose.e2e.yml, serves one.)
 */
import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const ROUTES = {
	"/auth/desktop/login": ["post"],
	"/auth/jwt/refresh": ["post"],
	"/auth/jwt/revoke": ["post"],
	"/users/me": ["get"],
	"/api/v1/workspaces": ["get"],
	"/api/v1/workspaces/{workspace_id}/notebooks": ["get", "post"],
	"/api/v1/workspaces/{workspace_id}/notebooks/search": ["get"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}": ["get", "patch", "delete"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/sources": ["get", "post"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/sources/{source_id}": ["patch", "delete"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/sources/toggle": ["post"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/sources/{source_id}/guide": ["get"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/research/import": ["post"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/threads": ["get", "post"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/followups": ["post"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/studio/outputs": ["get"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/studio/outputs/{output_id}": ["patch", "delete"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/studio/generate": ["post"],
	"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/studio/suggestions": ["get"],
	"/api/v1/workspaces/{workspace_id}/notebook-report-templates": ["get"],
	"/api/v1/workspaces/{workspace_id}/artifacts/{artifact_id}/manifest": ["get"],
	"/api/v1/workspaces/{workspace_id}/artifacts/{artifact_id}/download": ["get"],
	"/api/v1/workspaces/{workspace_id}/artifacts/{artifact_id}/files/{file_id}/content": ["get"],
	"/api/v1/threads/{thread_id}": ["get"],
	"/api/v1/threads/{thread_id}/cancel-active-turn": ["post"],
	"/api/v1/new_chat": ["post"],
	"/api/v1/documents/by-chunk/{chunk_id}": ["get"],
	"/api/v1/documents/fileupload": ["post"],
	"/api/v1/etl/capabilities": ["get"],
	"/api/v1/podcasts/languages": ["get"],
	"/api/v1/billing/me": ["get"],
};

function collectRefs(node, into) {
	if (Array.isArray(node)) for (const item of node) collectRefs(item, into);
	else if (node && typeof node === "object") {
		for (const [key, value] of Object.entries(node)) {
			if (key === "$ref" && typeof value === "string") into.add(value.split("/").pop());
			else collectRefs(value, into);
		}
	}
}

async function main() {
	const base = (process.argv[2] ?? "http://127.0.0.1:8000").replace(/\/+$/, "");
	const spec = await (await fetch(`${base}/openapi.json`)).json();
	const paths = {};
	for (const [path, methods] of Object.entries(ROUTES)) {
		const entry = spec.paths[path];
		if (!entry) throw new Error(`backend has no ${path}`);
		paths[path] = Object.fromEntries(methods.map((m) => [m, entry[m] ?? null]));
	}
	const wanted = new Set();
	collectRefs(paths, wanted);
	// Follow references transitively.
	let size = -1;
	while (wanted.size !== size) {
		size = wanted.size;
		for (const name of [...wanted]) collectRefs(spec.components.schemas[name], wanted);
	}
	const schemas = Object.fromEntries([...wanted].sort().map((name) => [name, spec.components.schemas[name]]));
	const out = join(
		dirname(fileURLToPath(import.meta.url)),
		"..",
		"src",
		"lib",
		"api",
		"__tests__",
		"openapi.snapshot.json"
	);
	writeFileSync(out, `${JSON.stringify({ paths, components: { schemas } }, null, "\t")}\n`);
	console.log(`wrote ${out}: ${Object.keys(paths).length} routes, ${Object.keys(schemas).length} schemas`);
}

main().catch((error) => {
	console.error(error.message);
	process.exit(1);
});
