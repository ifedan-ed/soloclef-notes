/**
 * Whether an error is the browser failing to load a part of the app's own
 * code: what a tab opened before a deploy meets when it needs code of its own
 * build that the server no longer has. A reload fixes it (the page then runs
 * the new build); nothing else does.
 *
 * Each bundler and browser words it differently, so all of them are listed,
 * each matched as a whole message (anchored at both ends), not as words
 * anywhere in one: a reload is only the answer to these failures, and an
 * error that merely quotes one (a server's message, a toast's text) must
 * reach the error page rather than reload the tab.
 *   - Turbopack (what `next build` uses): name ChunkLoadError,
 *     "Failed to load chunk <path> <why>", <why> being "from module <id>",
 *     "as a runtime dependency of chunk <path>", "from an HMR update" or,
 *     in older releases, "from runtime for chunk <path>"; then ": <cause>"
 *     when the fetch failed with an error of its own.
 *   - webpack: name ChunkLoadError, "Loading chunk <id> failed." and
 *     "Loading CSS chunk <id> failed.", each with a second line
 *     "(<type>: <url>)" or "(<url>)".
 *   - A failed `import()` of a module script: Chrome "Failed to fetch
 *     dynamically imported module: <url>", Firefox "error loading
 *     dynamically imported module" (with ": <url>" in newer releases),
 *     Safari "Importing a module script failed.".
 */
const CHUNK_MESSAGES = [
	/^Failed to load chunk \S+ (from module \S+|as a runtime dependency of chunk \S+|from runtime for chunk \S+|from an HMR update)(: [\s\S]*)?$/,
	/^Loading (CSS )?chunk \S+ failed\.(\n\([^\n]*\))?$/,
	/^Failed to fetch dynamically imported module: \S+$/,
	/^error loading dynamically imported module(: \S+)?$/,
	/^Importing a module script failed\.$/,
];

export function isChunkLoadError(error: unknown): boolean {
	// The failure can arrive wrapped by whatever awaited it; one level of
	// `cause` is where Turbopack and React put the original.
	for (let current = error, depth = 0; depth < 2; depth++) {
		if (!current || typeof current !== "object") return false;
		const { name, message, cause } = current as {
			name?: unknown;
			message?: unknown;
			cause?: unknown;
		};
		if (name === "ChunkLoadError") return true;
		if (typeof message === "string" && CHUNK_MESSAGES.some((pattern) => pattern.test(message))) {
			return true;
		}
		current = cause;
	}
	return false;
}
