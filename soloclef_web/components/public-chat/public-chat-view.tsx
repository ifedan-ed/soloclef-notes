"use client";

import { AssistantRuntimeProvider } from "@assistant-ui/react";
import { PublicHeader } from "@/components/site/public-header";
import { Spinner } from "@/components/ui/spinner";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import {
	PublicLinkUnavailable,
	PublicOutputView,
	publicLinkFailure,
	SharedNotice,
} from "@/features/public-sharing";
import { usePublicChat } from "@/hooks/use-public-chat";
import { usePublicChatRuntime } from "@/hooks/use-public-chat-runtime";
import { PublicThread } from "./public-thread";

interface PublicChatViewProps {
	shareToken: string;
}

/**
 * A public link, read-only: a shared chat or a shared Studio output. An
 * expired link says so; a revoked or unknown one says it is no longer there.
 */
export function PublicChatView({ shareToken }: PublicChatViewProps) {
	const { data, isLoading, error } = usePublicChat(shareToken);
	const runtime = usePublicChatRuntime({ data });
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

	if (data.kind === "output" && data.output) {
		// The viewer fills the window below the site's bar, as the owner's does.
		return (
			<main className="flex h-dvh flex-col overflow-hidden bg-main-panel text-foreground">
				<PublicHeader />
				<PublicOutputView
					shareToken={shareToken}
					output={data.output}
					expiresAt={data.expires_at}
				/>
			</main>
		);
	}

	return (
		<main className="min-h-screen bg-main-panel text-foreground overflow-x-hidden">
			<PublicHeader />
			<AssistantRuntimeProvider runtime={runtime}>
				<div className="flex h-screen pt-16 overflow-hidden">
					<div className="flex-1 flex flex-col min-w-0 overflow-hidden">
						<div className="mx-auto w-full max-w-[44rem] px-4 pt-4">
							<h1 className="truncate text-base font-semibold">{data.thread.title}</h1>
							<SharedNotice expiresAt={data.expires_at} />
						</div>
						<PublicThread />
					</div>
				</div>
			</AssistantRuntimeProvider>
		</main>
	);
}
