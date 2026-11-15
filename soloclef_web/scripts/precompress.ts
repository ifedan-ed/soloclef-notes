/**
 * Writes a Brotli (.br) and a gzip (.gz) copy beside every text file of the
 * static site, for Caddy to send as they are (`file_server { precompressed }`
 * in docker/proxy-go/routes.caddy).
 *
 * Why at build time: the Next.js server used to gzip each answer as it went
 * out. Compressing once here costs nothing per request, and Brotli at its
 * highest level, which is too slow to run per request, makes the scripts and
 * the docs search index a good deal smaller than gzip does.
 *
 *   pnpm exec tsx scripts/precompress.ts out
 */
import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { brotliCompressSync, constants, gzipSync } from "node:zlib";

/** Files that are text and worth compressing; pictures and video already are. */
const TEXT = /\.(html|txt|js|mjs|css|json|svg|xml|webmanifest|map)$/;

/** Below this, the copy saves less than its headers cost. */
const MIN_BYTES = 1024;

/** Whether a file of the export should get compressed copies. */
export function shouldCompress(file: string, size: number): boolean {
	// The public link page's template is filled in per request
	// (scripts/share-card-template.ts): a copy of the unfilled file would be
	// the wrong page.
	if (file.endsWith(".card.html")) return false;
	// The docs search index has no extension (out/api/search).
	const isText = TEXT.test(file) || file.endsWith(`${path.sep}api${path.sep}search`);
	return isText && size >= MIN_BYTES;
}

function* files(dir: string): Generator<string> {
	for (const entry of readdirSync(dir, { withFileTypes: true })) {
		const full = path.join(dir, entry.name);
		if (entry.isDirectory()) yield* files(full);
		else yield full;
	}
}

function main() {
	const outDir = process.argv[2];
	if (!outDir) throw new Error("usage: tsx scripts/precompress.ts <export dir>");
	let count = 0;
	let before = 0;
	let after = 0;
	for (const file of files(outDir)) {
		const { size } = statSync(file);
		if (!shouldCompress(file, size)) continue;
		const body = readFileSync(file);
		const br = brotliCompressSync(body, {
			params: {
				[constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY,
				[constants.BROTLI_PARAM_SIZE_HINT]: size,
			},
		});
		writeFileSync(`${file}.br`, br);
		writeFileSync(`${file}.gz`, gzipSync(body, { level: 9 }));
		count += 1;
		before += size;
		after += br.length;
	}
	const mb = (bytes: number) => (bytes / 1024 / 1024).toFixed(1);
	console.log(`compressed ${count} files: ${mb(before)} MB of text, ${mb(after)} MB as Brotli`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(__filename)) main();
