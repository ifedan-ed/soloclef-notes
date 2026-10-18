#!/usr/bin/env node
/**
 * Copy the web app's translations into the mobile bundle.
 *
 * The web's surfsense_web/messages/<locale>.json are the source of truth for
 * every string the two apps share. This script keeps only the namespaces the
 * app uses (so the bundle does not carry the whole web UI) and writes them to
 * src/lib/i18n/messages/. Run it after changing the web's messages:
 *
 *   npm run sync-messages          # write
 *   npm run sync-messages -- --check   # exit 1 if the copies are stale (CI)
 */
import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const LOCALES = ["en", "es", "pt", "hi", "ko", "zh"];
export const NAMESPACES = ["common", "auth", "notebooks", "upload_documents"];

const here = dirname(fileURLToPath(import.meta.url));
const webDir = join(here, "..", "..", "surfsense_web", "messages");
const outDir = join(here, "..", "src", "lib", "i18n", "messages");

export function extract(messages) {
	return Object.fromEntries(NAMESPACES.filter((ns) => ns in messages).map((ns) => [ns, messages[ns]]));
}

function main() {
	const check = process.argv.includes("--check");
	let stale = false;
	for (const locale of LOCALES) {
		const source = join(webDir, `${locale}.json`);
		if (!existsSync(source)) {
			console.error(`missing ${source}`);
			process.exit(1);
		}
		const picked = `${JSON.stringify(extract(JSON.parse(readFileSync(source, "utf8"))), null, "\t")}\n`;
		const target = join(outDir, `${locale}.json`);
		const current = existsSync(target) ? readFileSync(target, "utf8") : "";
		if (current === picked) continue;
		stale = true;
		if (check) console.error(`${target} is out of date; run npm run sync-messages`);
		else writeFileSync(target, picked);
	}
	if (check && stale) process.exit(1);
	if (!check) console.log(stale ? "messages updated" : "messages already up to date");
}

main();
