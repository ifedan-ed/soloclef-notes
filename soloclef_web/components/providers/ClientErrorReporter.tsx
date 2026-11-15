"use client";

import { useEffect } from "react";
import { recoverByReload } from "@/lib/chunk-reload/recover";
import { installGlobalClientErrorHandlers } from "@/lib/client-error-report";

/**
 * Reports uncaught errors and unhandled rejections to the backend log, after
 * a chunk that failed to load has had its chance to reload the page.
 */
export function ClientErrorReporter() {
	useEffect(() => {
		installGlobalClientErrorHandlers((kind, error) => recoverByReload(kind, error));
	}, []);

	return null;
}
