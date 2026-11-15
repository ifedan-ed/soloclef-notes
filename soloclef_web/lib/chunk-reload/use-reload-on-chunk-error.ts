"use client";

import { useEffect, useState } from "react";
import type { ClientErrorKind } from "@/lib/client-error-report";
import { recoverByReload, willRecoverByReload } from "./recover";

/**
 * For an error page: true while the page is reloading to recover from a
 * chunk that failed to load, when the page should show nothing (the reload
 * replaces it at once) and report nothing itself. Turns false if the reload
 * is refused after all, and the page then shows and reports the error as
 * usual.
 *
 * Decided in render without side effects, and acted on in an effect, so a
 * render React throws away cannot use up the build's one reload.
 */
export function useReloadOnChunkError(error: unknown, kind: ClientErrorKind): boolean {
	const [reloading, setReloading] = useState(() => willRecoverByReload(error));

	useEffect(() => {
		if (reloading && !recoverByReload(kind, error)) setReloading(false);
	}, [error, kind, reloading]);

	return reloading;
}
