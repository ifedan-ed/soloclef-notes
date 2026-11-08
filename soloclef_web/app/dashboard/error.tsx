"use client";

import Link from "next/link";
import { useEffect, useMemo } from "react";
import { Button } from "@/components/ui/button";
import { useSupportEmail } from "@/lib/brand/use-support-email";
import { reportClientError } from "@/lib/client-error-report";
import { buildIssueUrl } from "@/lib/error-toast";

export default function DashboardError({
	error,
	reset,
}: {
	error: globalThis.Error & { digest?: string; code?: string; requestId?: string };
	reset: () => void;
}) {
	useEffect(() => {
		reportClientError("error-boundary", error);
		import("posthog-js")
			.then(({ default: posthog }) => {
				posthog.captureException(error);
			})
			.catch(() => {});
	}, [error]);

	const supportEmail = useSupportEmail();
	const issueUrl = useMemo(() => buildIssueUrl(error, supportEmail), [error, supportEmail]);

	return (
		<div className="flex flex-1 flex-col items-center justify-center gap-4 p-8 text-center">
			<h2 className="text-xl font-semibold">Something went wrong</h2>
			<p className="text-muted-foreground max-w-md">
				An error occurred in this section. Your dashboard is still available. If this keeps
				happening, please report it so we can fix it.
			</p>

			{(error.digest || error.code || error.requestId) && (
				<div className="rounded-md border bg-muted/50 px-4 py-2 text-xs text-muted-foreground font-mono max-w-md">
					{error.code && <span>Code: {error.code}</span>}
					{error.requestId && <span className="ml-3">ID: {error.requestId}</span>}
					{error.digest && <span className="ml-3">Digest: {error.digest}</span>}
				</div>
			)}

			<div className="flex gap-2">
				<Button type="button" onClick={reset}>
					Try again
				</Button>
				<Button asChild variant="ghost">
					<Link href="/dashboard">Back to dashboard</Link>
				</Button>
				<Button asChild variant="ghost">
					<a href={issueUrl}>Report Issue</a>
				</Button>
			</div>
		</div>
	);
}
