"use client";

import "./globals.css";
import posthog from "posthog-js";
import { useEffect, useMemo } from "react";
import { Button } from "@/components/ui/button";
import { buildReportMailto } from "@/lib/brand/report";
import { useSupportEmail } from "@/lib/brand/use-support-email";
import { useReloadOnChunkError } from "@/lib/chunk-reload/use-reload-on-chunk-error";
import { reportClientError } from "@/lib/client-error-report";
import { detectEnvironment } from "@/lib/env-config";

function buildBasicIssueUrl(error: Error & { digest?: string }, to: string) {
	const lines = [
		"## Bug Report",
		"",
		"**Describe what happened:**",
		"",
		"",
		"## Diagnostics (auto-filled)",
		"",
		`- **Error:** ${error.message}`,
		...(error.digest ? [`- **Digest:** \`${error.digest}\``] : []),
		`- **Environment:** ${detectEnvironment()}`,
		`- **Timestamp:** ${new Date().toISOString()}`,
		`- **Page:** \`${typeof window !== "undefined" ? window.location.pathname : "unknown"}\``,
		`- **User Agent:** \`${typeof navigator !== "undefined" ? navigator.userAgent : "unknown"}\``,
	];
	return buildReportMailto(lines, undefined, to);
}

export default function GlobalError({
	error,
	reset,
}: {
	error: Error & { digest?: string };
	reset: () => void;
}) {
	// The root layout's own code failing to load: reload into the new build
	// (lib/chunk-reload) rather than show this page.
	const reloading = useReloadOnChunkError(error, "global-error");

	useEffect(() => {
		if (reloading) return;
		reportClientError("global-error", error);
		posthog.captureException(error);
	}, [error, reloading]);

	const supportEmail = useSupportEmail();
	const issueUrl = useMemo(() => buildBasicIssueUrl(error, supportEmail), [error, supportEmail]);

	if (reloading) {
		// This replaces the whole document, so it must still be one.
		return (
			<html lang="en">
				<body />
			</html>
		);
	}

	return (
		<html lang="en">
			<body>
				<div className="flex min-h-screen flex-col items-center justify-center gap-4 p-4 text-center">
					<h2 className="text-xl font-semibold">Something went wrong</h2>
					<p className="text-sm text-muted-foreground max-w-md">
						An unexpected error occurred. Please try again, or report this issue if it persists.
					</p>

					{error.digest && (
						<p className="text-xs text-muted-foreground font-mono">Digest: {error.digest}</p>
					)}

					<div className="flex gap-2">
						<Button onClick={reset}>Try again</Button>
						<a
							href={issueUrl}
							className="inline-flex items-center gap-1.5 rounded-md border border-input bg-background px-4 py-2 text-sm font-medium hover:bg-accent hover:text-accent-foreground transition-colors"
						>
							Report issue
						</a>
					</div>
				</div>
			</body>
		</html>
	);
}
