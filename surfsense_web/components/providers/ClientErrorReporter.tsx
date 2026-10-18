"use client";

import { useEffect } from "react";
import { installGlobalClientErrorHandlers } from "@/lib/client-error-report";

/** Reports uncaught errors and unhandled rejections to the backend log. */
export function ClientErrorReporter() {
	useEffect(() => {
		installGlobalClientErrorHandlers();
	}, []);

	return null;
}
