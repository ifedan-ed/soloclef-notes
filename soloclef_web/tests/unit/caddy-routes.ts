// The deployment's Caddy routes (docker/proxy-go/routes.caddy), read as text
// for the unit tests that check a rule Next.js used to hold and Caddy holds
// now. Not a test file itself (tests/unit/**/*.test.ts are).
import { readFileSync } from "node:fs";
import path from "node:path";

export const ROUTES_CADDY = readFileSync(
	path.resolve(__dirname, "../../../docker/proxy-go/routes.caddy"),
	"utf8"
);

/** The lines of the file with comments and blank lines removed, trimmed. */
export function caddyLines(text: string = ROUTES_CADDY): string[] {
	return text
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line && !line.startsWith("#"));
}
