"use client";

import "./globals.css";
import posthog from "posthog-js";
import { useEffect, useMemo } from "react";
import { Button } from "@/components/ui/button";
import { buildReportMailto } from "@/lib/brand/report";
import { useSupportEmail } from "@/lib/brand/use-support-email";
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
	useEffect(() => {
		reportClientError("global-error", error);
		posthog.captureException(error);
	}, [error]);

	const supportEmail = useSupportEmail();
	const issueUrl = useMemo(() => buildBasicIssueUrl(error, supportEmail), [error, supportEmail]);

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
							Report Issue
						</a>
					</div>
				</div>
			</body>
		</html>
	);
}
