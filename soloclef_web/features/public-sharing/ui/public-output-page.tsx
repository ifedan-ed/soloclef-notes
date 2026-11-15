"use client";

import { PublicHeader } from "@/components/site/public-header";
import { Spinner } from "@/components/ui/spinner";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { usePublicOutput } from "../hooks/use-public-output";
import { publicLinkFailure } from "../lib/public-link-failure";
import { PublicLinkUnavailable } from "./public-link-unavailable";
import { PublicOutputView } from "./public-output-view";

/**
 * A public link: one shared Studio output, read-only. An expired link says
 * so; a revoked or unknown one says it is no longer there.
 */
export function PublicOutputPage({ shareToken }: { shareToken: string }) {
	const { data, isLoading, error } = usePublicOutput(shareToken);
	const showSpinner = useDelayedFlag(isLoading);

	if (isLoading) {
		return (
			<main className="min-h-screen bg-main-panel text-foreground overflow-x-hidden">
				<PublicHeader />
				<div className="flex h-screen items-center justify-center">
					{showSpinner ? <Spinner size="lg" className="text-muted-foreground" /> : null}
				</div>
			</main>
		);
	}

	if (error || !data) {
		return <PublicLinkUnavailable reason={error ? publicLinkFailure(error) : "gone"} />;
	}

	// The viewer fills the window below the site's bar, as the owner's does.
	return (
		<main className="flex h-dvh flex-col overflow-hidden bg-main-panel text-foreground">
			<PublicHeader />
			<PublicOutputView shareToken={shareToken} output={data.output} expiresAt={data.expires_at} />
		</main>
	);
}
