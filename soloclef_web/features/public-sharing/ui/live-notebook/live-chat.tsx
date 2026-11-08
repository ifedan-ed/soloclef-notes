"use client";

import {
	AssistantRuntimeProvider,
	type ToolCallMessagePartProps,
	useExternalStoreRuntime,
} from "@assistant-ui/react";
import { useTranslations } from "next-intl";
import { createContext, useCallback, useContext, useMemo } from "react";
import {
	PublicCitationsProvider,
	type PublicCitationTarget,
} from "@/components/assistant-ui/public-citations";
import { PublicThread } from "@/components/public-chat/public-thread";
import { ArtifactFormatIcon } from "@/features/artifacts/ui/artifact-format-icon";
import { ArtifactFormatLabel } from "@/features/artifacts/ui/artifact-format-label";
import { convertToThreadMessage } from "@/lib/chat/message-utils";
import type { LiveCitation, LiveMessage } from "../../model/live-notebook";

interface MadeInChat {
	artifact_id: number;
	title: string;
	format: string;
}

const OpenArtifactContext = createContext<(artifact: MadeInChat) => void>(() => {});

/** Something the chat made (a saved file, a podcast, a video), opened in the viewer. */
function MadeInChatCard({ result }: ToolCallMessagePartProps<unknown, MadeInChat>) {
	const open = useContext(OpenArtifactContext);
	const t = useTranslations("publicSharing.live");
	if (!result) return null;
	return (
		<button
			type="button"
			onClick={() => open(result)}
			className="my-4 flex w-full items-center gap-3 rounded-xl border bg-muted/30 p-4 text-left transition-colors hover:bg-accent"
			data-testid="live-chat-artifact"
		>
			<span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted">
				<ArtifactFormatIcon format={result.format} className="size-5 text-muted-foreground" />
			</span>
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm font-medium">{result.title}</span>
				<span className="mt-0.5 flex gap-1 text-xs text-muted-foreground">
					<ArtifactFormatLabel format={result.format} />
					<span aria-hidden>·</span>
					{t("made_in_chat")}
				</span>
			</span>
		</button>
	);
}

const BODY_TOOLS = { notebook_artifact: MadeInChatCard } as const;

/**
 * The shared notebook's chat, read-only: no composer, no edit, no reruns.
 * Messages come from the link's latest read, so a new turn appears in place.
 */
export function LiveChat({
	messages,
	citations,
	onOpenSource,
	onOpenArtifact,
}: {
	messages: LiveMessage[];
	citations: Record<string, LiveCitation>;
	onOpenSource: (citation: LiveCitation) => void;
	onOpenArtifact: (artifact: MadeInChat) => void;
}) {
	const t = useTranslations("publicSharing.live");
	const convertMessage = useCallback(
		(message: LiveMessage, index: number) =>
			convertToThreadMessage({
				id: index,
				thread_id: 0,
				role: message.role,
				content: message.content,
				created_at: message.created_at ?? new Date(0).toISOString(),
				author_id: null,
				author_display_name: null,
				author_avatar_url: null,
			}),
		[]
	);
	const runtime = useExternalStoreRuntime({
		isRunning: false,
		messages,
		onNew: async () => {},
		convertMessage,
	});
	const resolve = useCallback(
		(number: number): PublicCitationTarget | null => {
			const citation = citations[String(number)];
			if (!citation) return null;
			return {
				label: citation.page
					? t("cited", { title: citation.title, page: citation.page })
					: citation.title,
				open: () => onOpenSource(citation),
			};
		},
		[citations, onOpenSource, t]
	);
	const open = useMemo(() => onOpenArtifact, [onOpenArtifact]);

	if (messages.length === 0) {
		return (
			<div className="flex h-full items-center justify-center p-6 text-center">
				<p className="text-sm text-muted-foreground">{t("no_chat")}</p>
			</div>
		);
	}
	return (
		<AssistantRuntimeProvider runtime={runtime}>
			<PublicCitationsProvider resolve={resolve}>
				<OpenArtifactContext.Provider value={open}>
					<PublicThread bodyTools={BODY_TOOLS} />
				</OpenArtifactContext.Provider>
			</PublicCitationsProvider>
		</AssistantRuntimeProvider>
	);
}
