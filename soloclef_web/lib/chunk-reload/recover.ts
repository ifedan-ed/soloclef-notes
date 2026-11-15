/**
 * A page that fails to load its own code reloads itself once, so that a tab
 * opened before a deploy moves on to the new build instead of showing
 * "Something went wrong". The server keeps earlier builds' files for a while
 * (soloclef_web/site-image/kept-assets.sh), so this is for a tab older than
 * that, or a file that is missing for another reason.
 *
 * The failure is still reported, marked as recovered by a reload, so the log
 * shows how often it happens without it reading as a crash.
 */
import { type ClientErrorKind, reportClientError } from "@/lib/client-error-report";
import { isChunkLoadError } from "./is-chunk-load-error";
import { claimReload, mayReload, tabStorage } from "./reload-guard";

/**
 * This build's own id, made once per image build (the Dockerfile's builder
 * stage), so that the reload guard tells one deploy from the next. Empty
 * under `next dev`, which is not a build; the guard then counts every reload
 * as the same build's, which is what a developer's tab is.
 */
export const BUILD_ID = process.env.NEXT_PUBLIC_BUILD_ID ?? "";

export interface ReloadDeps {
	store: Storage | null;
	now: number;
	reload: () => void;
	report: typeof reportClientError;
}

function browserDeps(): ReloadDeps {
	return {
		store: tabStorage(),
		now: Date.now(),
		reload: () => window.location.reload(),
		report: reportClientError,
	};
}

/**
 * Whether this error will be handled by a reload, without doing it: what an
 * error page renders by, before its effect runs `recoverByReload`.
 */
export function willRecoverByReload(
	error: unknown,
	deps: Pick<ReloadDeps, "store" | "now"> = browserDeps()
): boolean {
	return isChunkLoadError(error) && mayReload(deps.store, BUILD_ID, deps.now);
}

/**
 * Reload for a chunk that failed to load, if this build has not already done
 * so just now. True when the page is reloading: the caller shows nothing and
 * reports nothing more. False means the error is the caller's as usual.
 */
export function recoverByReload(
	kind: ClientErrorKind,
	error: unknown,
	deps: ReloadDeps = browserDeps()
): boolean {
	if (!isChunkLoadError(error) || !claimReload(deps.store, BUILD_ID, deps.now)) return false;
	// Sent with keepalive (client-error-report), so the reload does not cancel it.
	deps.report(kind, error, { recoveredByReload: true });
	deps.reload();
	return true;
}
