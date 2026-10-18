import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import { fallbackEtlService } from "@/lib/etl/fallback-service";
import { getSupportedExtensions } from "@/lib/supported-extensions";

// Run with: pnpm exec tsx --test tests/unit/documents/fallback-etl-service.test.ts

test("the default parser picks the table: Local is pymupdf4llm, Cloud its service", () => {
	assert.equal(fallbackEtlService({ ETL_DEFAULT_PARSER: "LOCAL" }), "PYMUPDF4LLM");
	assert.equal(
		fallbackEtlService({ ETL_DEFAULT_PARSER: "cloud", ETL_CLOUD_SERVICE: " datalab " }),
		"DATALAB"
	);
	assert.equal(fallbackEtlService({ ETL_DEFAULT_PARSER: "CLOUD" }), "LLAMACLOUD");
	// Unset ETL_DEFAULT_PARSER means LOCAL, as in the backend.
	assert.equal(fallbackEtlService({ ETL_CLOUD_SERVICE: "DATALAB" }), "PYMUPDF4LLM");
});

test("this server's lines: the stale ETL_SERVICE is ignored beside ETL_CLOUD_SERVICE", () => {
	assert.equal(
		fallbackEtlService({
			ETL_SERVICE: "LLAMACLOUD",
			ETL_CLOUD_SERVICE: "DATALAB",
			ETL_DEFAULT_PARSER: "CLOUD",
		}),
		"DATALAB"
	);
});

test("alone, the deprecated ETL_SERVICE names Cloud only when it names a cloud service", () => {
	assert.equal(
		fallbackEtlService({ ETL_SERVICE: "UNSTRUCTURED", ETL_DEFAULT_PARSER: "CLOUD" }),
		"UNSTRUCTURED"
	);
	assert.equal(
		fallbackEtlService({ ETL_SERVICE: "PYMUPDF4LLM", ETL_DEFAULT_PARSER: "CLOUD" }),
		"LLAMACLOUD"
	);
	// A pre-split server that set only ETL_SERVICE now defaults to Local.
	assert.equal(fallbackEtlService({ ETL_SERVICE: "LLAMACLOUD" }), "PYMUPDF4LLM");
});

test("with nothing set (compose passes empty strings) the build-time fallback applies", () => {
	assert.equal(fallbackEtlService({}), undefined);
	assert.equal(
		fallbackEtlService({ ETL_DEFAULT_PARSER: "", ETL_CLOUD_SERVICE: "", ETL_SERVICE: " " }),
		undefined
	);
});

// Types every table carries whatever the parser: read as text, converted
// directly, or transcribed. The rest must match the backend's Datalab list.
const PARSER_AGNOSTIC = new Set([
	".md",
	".markdown",
	".txt",
	".html",
	".htm",
	".csv",
	".tsv",
	".mp3",
	".mpeg",
	".mpga",
	".mp4",
	".m4a",
	".wav",
	".webm",
]);
const BACKEND_EXTENSIONS = path.resolve(
	__dirname,
	"../../../../surfsense_backend/app/utils/file_extensions.py"
);

test("the Datalab table offers exactly the backend's Datalab document types", {
	skip: !existsSync(BACKEND_EXTENSIONS) && "the backend is not in this checkout",
}, () => {
	const source = readFileSync(BACKEND_EXTENSIONS, "utf8");
	const block = source.match(/DATALAB_DOCUMENT_EXTENSIONS[^{]*\{([^}]*)\}/);
	assert.ok(block, "DATALAB_DOCUMENT_EXTENSIONS is in file_extensions.py");
	const backend = [...block[1].matchAll(/"(\.[a-z0-9]+)"/g)].map((m) => m[1]).sort();
	const offered = getSupportedExtensions(undefined, "DATALAB").filter(
		(ext) => !PARSER_AGNOSTIC.has(ext)
	);
	assert.deepEqual(offered, backend);
});
