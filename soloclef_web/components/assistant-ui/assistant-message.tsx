import {
	ActionBarMorePrimitive,
	ActionBarPrimitive,
	ErrorPrimitive,
	MessagePrimitive,
	useAuiState,
} from "@assistant-ui/react";
import { useAtomValue } from "jotai";
import {
	CheckIcon,
	ClipboardPaste,
	CopyIcon,
	Dot,
	DownloadIcon,
	ExternalLink,
	Globe,
	MoreHorizontalIcon,
	RefreshCwIcon,
} from "lucide-react";
import dynamic from "next/dynamic";
import type { FC } from "react";
import { useMemo, useState } from "react";
import {
	globalModelConnectionsAtom,
	modelConnectionsAtom,
} from "@/atoms/model-connections/model-connections-query.atoms";
import {
	CitationMetadataProvider,
	useAllCitationMetadata,
} from "@/components/assistant-ui/citation-metadata-context";
import { MessageTimestamp } from "@/components/assistant-ui/message-timestamp";
import { RevertTurnButton } from "@/components/assistant-ui/revert-turn-button";
import {
	type TokenUsageModelBreakdown,
	useTokenUsage,
} from "@/components/assistant-ui/token-usage-context";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import type { SerializableCitation } from "@/components/tool-ui/citation";
import { LegacyDeliverableToolUI } from "@/components/tool-ui/legacy-deliverable";
import {
	openSafeNavigationHref,
	resolveSafeNavigationHref,
} from "@/components/tool-ui/shared/media";
import { Button } from "@/components/ui/button";
import {
	Drawer,
	DrawerContent,
	DrawerHandle,
	DrawerHeader,
	DrawerTitle,
} from "@/components/ui/drawer";
import { DropdownMenuLabel } from "@/components/ui/dropdown-menu";
import { withArtifactAnchor } from "@/features/chat-artifacts";
import { InterleavedMessageParts } from "@/features/chat-messages/timeline";
import { NotebookFollowups } from "@/features/notebooks/ui/chat/notebook-followups";
import { SaveToNoteButton } from "@/features/notebooks/ui/save-to-note-button";
import { useMediaQuery } from "@/hooks/use-media-query";
import { useElectronAPI } from "@/hooks/use-platform";
import { formatMessageTimestamp } from "@/lib/format-date";
import { getProviderIcon } from "@/lib/provider-icons";
import { tryGetHostname } from "@/lib/url";
import { cn, copyToClipboard } from "@/lib/utils";

// Captured once at module load — survives client-side navigations that strip the query param.
const IS_QUICK_ASSIST_WINDOW =
	typeof window !== "undefined" &&
	new URLSearchParams(window.location.search).get("quickAssist") === "true";

// Dynamically import tool UI components to avoid loading them in main bundle
const SaveArtifactToolUI = dynamic(
	() =>
		import("@/components/tool-ui/save-artifact").then((m) => ({
			default: m.SaveArtifactToolUI,
		})),
	{ ssr: false }
);
const GeneratePodcastToolUI = dynamic(
	() =>
		import("@/components/tool-ui/podcast").then((m) => ({
			default: m.GeneratePodcastToolUI,
		})),
	{ ssr: false }
);
const GenerateVideoPresentationToolUI = dynamic(
	() =>
		import("@/components/tool-ui/video-presentation").then((m) => ({
			default: m.GenerateVideoPresentationToolUI,
		})),
	{ ssr: false }
);
const EnqueueDeliverableJobToolUI = dynamic(
	() =>
		import("@/components/tool-ui/deliverable-job").then((m) => ({
			default: m.EnqueueDeliverableJobToolUI,
		})),
	{ ssr: false }
);
const PageReaderToolUI = dynamic(
	() =>
		import("@/features/page-reader/ui/page-reader-card").then((m) => ({
			default: m.PageReaderToolUI,
		})),
	{ ssr: false }
);
const GenerateImageToolUI = dynamic(
	() =>
		import("@/components/tool-ui/generate-image").then((m) => ({ default: m.GenerateImageToolUI })),
	{ ssr: false }
);
function useCitationsFromMetadata(): SerializableCitation[] {
	const allCitations = useAllCitationMetadata();
	return useMemo(() => {
		const result: SerializableCitation[] = [];
		for (const [url, meta] of allCitations) {
			const domain = tryGetHostname(url);
			result.push({
				id: `url-cite-${url}`,
				href: url,
				title: meta.title,
				snippet: meta.snippet,
				domain,
				favicon: domain ? `https://www.google.com/s2/favicons?domain=${domain}&sz=32` : undefined,
				type: "webpage",
			});
		}
		return result;
	}, [allCitations]);
}

const MobileCitationDrawer: FC = () => {
	const [open, setOpen] = useState(false);
	const citations = useCitationsFromMetadata();

	if (citations.length === 0) return null;

	const maxIcons = 4;
	const visible = citations.slice(0, maxIcons);
	const remainingCount = Math.max(0, citations.length - maxIcons);

	const handleNavigate = (citation: SerializableCitation) => {
		const href = resolveSafeNavigationHref(citation.href);
		if (href) openSafeNavigationHref(href);
	};

	return (
		<>
			<Button
				type="button"
				variant="ghost"
				onClick={() => setOpen(true)}
				className={cn(
					"isolate h-auto cursor-pointer gap-2 rounded-lg px-3 py-2",
					"bg-muted/40 outline-none",
					"transition-colors duration-150",
					"hover:bg-accent hover:text-accent-foreground",
					"focus-visible:ring-ring focus-visible:ring-2"
				)}
			>
				<div className="flex items-center">
					{visible.map((citation, index) => (
						<div
							key={citation.id}
							className={cn(
								"border-border bg-background dark:border-foreground/20 relative flex size-6 items-center justify-center rounded-full border shadow-xs",
								index > 0 && "-ml-2"
							)}
							style={{ zIndex: maxIcons - index }}
						>
							{citation.favicon ? (
								// biome-ignore lint/performance/noImgElement: external favicon from arbitrary domain
								<img
									src={citation.favicon}
									alt=""
									aria-hidden="true"
									width={18}
									height={18}
									className="size-4.5 rounded-full object-cover"
								/>
							) : (
								<Globe className="text-muted-foreground size-3" aria-hidden="true" />
							)}
						</div>
					))}
					{remainingCount > 0 && (
						<div
							className="border-border bg-background dark:border-foreground/20 relative -ml-2 flex size-6 items-center justify-center rounded-full border shadow-xs"
							style={{ zIndex: 0 }}
						>
							<span className="text-muted-foreground text-[10px] font-medium tracking-tight">
								•••
							</span>
						</div>
					)}
				</div>
				<span className="text-muted-foreground text-sm tabular-nums">
					{citations.length} source{citations.length !== 1 && "s"}
				</span>
			</Button>

			<Drawer open={open} onOpenChange={setOpen}>
				<DrawerContent className="max-h-[85vh] flex flex-col">
					<DrawerHandle />
					<DrawerHeader className="text-left">
						<DrawerTitle className="text-base font-semibold">Sources</DrawerTitle>
					</DrawerHeader>
					<div className="overflow-y-auto flex-1 min-h-0 px-1 pb-6">
						{citations.map((citation) => (
							<Button
								key={citation.id}
								type="button"
								variant="ghost"
								onClick={() => handleNavigate(citation)}
								className="group h-auto w-full justify-start gap-2.5 px-3 py-2.5 text-left hover:bg-accent hover:text-accent-foreground focus-visible:bg-muted"
							>
								{citation.favicon ? (
									// biome-ignore lint/performance/noImgElement: external favicon from arbitrary domain
									<img
										src={citation.favicon}
										alt=""
										aria-hidden="true"
										width={16}
										height={16}
										className="bg-muted size-4 shrink-0 rounded object-cover"
									/>
								) : (
									<Globe className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
								)}
								<div className="min-w-0 flex-1">
									<p className="truncate text-sm font-medium group-hover:underline group-hover:underline-offset-2">
										{citation.title}
									</p>
									<p className="text-muted-foreground truncate text-xs">{citation.domain}</p>
								</div>
								<ExternalLink className="text-muted-foreground size-3.5 shrink-0 opacity-0 transition-opacity group-hover:opacity-100" />
							</Button>
						))}
					</div>
				</DrawerContent>
			</Drawer>
		</>
	);
};

export const MessageError: FC = () => {
	return (
		<MessagePrimitive.Error>
			<ErrorPrimitive.Root className="aui-message-error-root mt-2 rounded-md border border-destructive bg-destructive/10 p-3 text-destructive text-sm dark:bg-destructive/5 dark:text-red-200">
				<ErrorPrimitive.Message className="aui-message-error-message line-clamp-2" />
			</ErrorPrimitive.Root>
		</MessagePrimitive.Error>
	);
};

/**
 * Format provider USD cost (in micro-USD) for inline display next to a
 * token count. Falls back to ``"<$0.001"`` for sub-tenth-of-a-cent
 * costs so a real-but-tiny figure doesn't render as ``$0.000``.
 */
function formatTurnCost(micros: number): string {
	const dollars = micros / 1_000_000;
	if (dollars >= 1) return `$${dollars.toFixed(2)}`;
	if (dollars >= 0.01) return `$${dollars.toFixed(3)}`;
	if (dollars > 0) return "<$0.001";
	return "$0";
}

function normalizeUsageModelKey(modelKey: string): string {
	return modelKey.trim().replace(/^~/, "");
}

function bareModelKey(modelKey: string): string {
	const normalized = normalizeUsageModelKey(modelKey);
	const parts = normalized.split("/");
	return parts[parts.length - 1] || normalized;
}

function inferProviderFromModelKey(modelKey: string) {
	const normalized = normalizeUsageModelKey(modelKey);
	const [provider] = normalized.split("/");
	return provider && provider !== normalized ? provider : null;
}

function titleCaseModelPart(part: string) {
	if (!part) return "";
	const upper = part.toUpperCase();
	if (/^\d+(\.\d+)?[BKM]$/.test(upper)) return upper;
	if (["gpt", "oai", "api", "llm", "vlm"].includes(part.toLowerCase())) return upper;
	return part.charAt(0).toUpperCase() + part.slice(1);
}

function humanizeModelId(modelKey: string): string {
	const bare = bareModelKey(modelKey)
		.replace(/:latest$/i, "")
		.replace(/[-_]+/g, " ")
		.trim();
	if (!bare) return modelKey;
	return bare.split(/\s+/).map(titleCaseModelPart).join(" ");
}

const MessageInfoDropdown: FC<{ chatTurnId: string | null | undefined }> = ({ chatTurnId }) => {
	const messageId = useAuiState(({ message }) => message?.id);
	const createdAt = useAuiState(({ message }) => message?.createdAt);
	const usage = useTokenUsage(messageId);

	const { data: globalConnections = [] } = useAtomValue(globalModelConnectionsAtom);
	const { data: localConnections = [] } = useAtomValue(modelConnectionsAtom);

	const modelConnectionByKey = useMemo(() => {
		const map = new Map<
			string,
			{ name: string; provider: string; modelId: string; shared: boolean }
		>();
		// Shared models last, so their friendly names win over a raw id.
		for (const connection of [...localConnections, ...globalConnections]) {
			for (const model of connection.models) {
				const normalizedModelId = normalizeUsageModelKey(model.model_id);
				const entry = {
					name: model.display_name || model.model_id,
					provider: connection.provider,
					modelId: model.model_id,
					shared: connection.scope === "global",
				};
				map.set(model.model_id, entry);
				map.set(normalizedModelId, entry);
				map.set(bareModelKey(model.model_id), entry);
			}
		}
		return map;
	}, [globalConnections, localConnections]);

	const resolveModel = (modelKey: string, counts: TokenUsageModelBreakdown) => {
		const normalizedKey = normalizeUsageModelKey(counts.model_id || counts.model || modelKey);
		const connectionModel =
			modelConnectionByKey.get(modelKey) ??
			modelConnectionByKey.get(normalizeUsageModelKey(modelKey)) ??
			modelConnectionByKey.get(normalizedKey) ??
			modelConnectionByKey.get(bareModelKey(normalizedKey));
		const provider =
			counts.provider || connectionModel?.provider || inferProviderFromModelKey(normalizedKey);
		const modelId = counts.model_id || connectionModel?.modelId || modelKey;
		// A shared model is shown under today's name, not the one stored with an
		// older message, so a rename reaches past answers too.
		const sharedName = connectionModel?.shared ? connectionModel.name : null;
		const name =
			sharedName || counts.display_name || connectionModel?.name || humanizeModelId(modelId);
		return {
			name,
			modelId,
			icon: provider ? getProviderIcon(provider, { className: "size-3.5 shrink-0" }) : null,
		};
	};

	const modelBreakdown = usage ? (usage.usage ?? usage.model_breakdown) : undefined;
	const models = modelBreakdown ? Object.entries(modelBreakdown) : [];
	const hasUsage = usage && usage.total_tokens > 0;

	return (
		<ActionBarMorePrimitive.Root>
			<ActionBarMorePrimitive.Trigger asChild>
				<Button variant="ghost" size="icon" className="aui-button-icon size-6 p-1">
					<MoreHorizontalIcon className="size-4" />
					<span className="sr-only">More</span>
				</Button>
			</ActionBarMorePrimitive.Trigger>
			<ActionBarMorePrimitive.Content
				align="start"
				className="bg-popover text-popover-foreground z-50 max-h-(--radix-dropdown-menu-content-available-height) min-w-[180px] origin-(--radix-dropdown-menu-content-transform-origin) overflow-x-hidden overflow-y-auto rounded-md p-1 shadow-md data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2"
			>
				{createdAt && (
					<DropdownMenuLabel className="text-xs text-muted-foreground font-normal select-none">
						{formatMessageTimestamp(createdAt)}
					</DropdownMenuLabel>
				)}
				{hasUsage && (
					<>
						<ActionBarMorePrimitive.Separator className="bg-popover-border mx-1 my-1 h-px" />
						{models.length > 0 ? (
							models.map(([model, counts]) => {
								const { name, icon } = resolveModel(model, counts);
								const costMicros = counts.cost_micros;
								return (
									<ActionBarMorePrimitive.Item
										key={model}
										className="focus:bg-accent focus:text-accent-foreground relative flex cursor-default flex-col items-start gap-1 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none"
										onSelect={(e) => e.preventDefault()}
									>
										<span className="flex items-center gap-1.5 text-xs font-medium">
											{icon}
											{name}
										</span>
										<span className="flex items-center text-xs text-muted-foreground">
											<span>{counts.total_tokens.toLocaleString()} tokens</span>
											{costMicros && costMicros > 0 ? (
												<>
													<Dot className="size-4 shrink-0" aria-hidden="true" />
													<span>{formatTurnCost(costMicros)}</span>
												</>
											) : null}
										</span>
									</ActionBarMorePrimitive.Item>
								);
							})
						) : (
							<ActionBarMorePrimitive.Item
								className="focus:bg-accent focus:text-accent-foreground relative flex cursor-default flex-col items-start gap-0.5 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none"
								onSelect={(e) => e.preventDefault()}
							>
								<span className="flex items-center text-xs text-muted-foreground">
									<span>{usage.total_tokens.toLocaleString()} tokens</span>
									{usage.cost_micros && usage.cost_micros > 0 ? (
										<>
											<Dot className="size-4 shrink-0" aria-hidden="true" />
											<span>{formatTurnCost(usage.cost_micros)}</span>
										</>
									) : null}
								</span>
							</ActionBarMorePrimitive.Item>
						)}
					</>
				)}
				<RevertTurnButton chatTurnId={chatTurnId} variant="menu-item" />
			</ActionBarMorePrimitive.Content>
		</ActionBarMorePrimitive.Root>
	);
};

/**
 * Tools rendered in the message BODY — value-add deliverables only.
 *
 * Process tools (connector CRUD, sandbox execute, memory updates,
 * etc.) are grouped into chronological trace segments. This map is the
 * single frontend boundary between rich deliverable cards and opaque
 * backend-owned process activities.
 */
const BODY_TOOLS = {
	save_artifact: withArtifactAnchor(SaveArtifactToolUI),
	generate_report: LegacyDeliverableToolUI,
	generate_resume: LegacyDeliverableToolUI,
	generate_podcast: withArtifactAnchor(GeneratePodcastToolUI),
	generate_video_presentation: withArtifactAnchor(GenerateVideoPresentationToolUI),
	enqueue_deliverable_job: withArtifactAnchor(EnqueueDeliverableJobToolUI),
	display_image: withArtifactAnchor(GenerateImageToolUI),
	generate_image: withArtifactAnchor(GenerateImageToolUI),
	// Not a deliverable, but what the answer rests on: the pages it read.
	web_crawl: PageReaderToolUI,
} as const;

const AssistantMessageInner: FC = () => {
	const isMobile = !useMediaQuery("(min-width: 768px)");

	return (
		<CitationMetadataProvider>
			<div className="aui-assistant-message-content wrap-break-word px-2 text-foreground leading-relaxed">
				<InterleavedMessageParts bodyTools={BODY_TOOLS} />
				<MessageError />
			</div>

			<MessageTimestamp className="ml-2 mt-2" />

			{isMobile && (
				<div className="ml-2 mt-2">
					<MobileCitationDrawer />
				</div>
			)}

			<div className="aui-assistant-message-footer mt-3 mb-5 ml-2 h-6">
				<div className="h-full opacity-100 transition-opacity">
					<AssistantActionBar />
				</div>
			</div>
			<NotebookFollowups />
		</CitationMetadataProvider>
	);
};

export const AssistantMessage: FC = () => (
	<MessagePrimitive.Root
		className="aui-assistant-message-root group fade-in slide-in-from-bottom-1 relative mx-auto w-full max-w-(--thread-max-width) animate-in py-3 duration-150"
		data-role="assistant"
	>
		<AssistantMessageInner />
	</MessagePrimitive.Root>
);

const AssistantActionBar: FC = () => {
	const isLast = useAuiState((s) => s.message.isLast);
	const content = useAuiState((s) => s.message.content);
	const [copiedAnswer, setCopiedAnswer] = useState(false);
	const api = useElectronAPI();
	// Surface the persisted ``chat_turn_id`` so the per-turn revert
	// affordance can scope to just this message's actions. Streamed
	// turns get their id once the assistant message is hydrated/finalised.
	const chatTurnId = useAuiState(({ message }) => {
		const meta = message?.metadata as { custom?: { chatTurnId?: string | null } } | undefined;
		return meta?.custom?.chatTurnId ?? null;
	});

	const isQuickAssist = !!api?.replaceText && IS_QUICK_ASSIST_WINDOW;
	const answerText = Array.isArray(content)
		? content
				.filter(
					(part): part is { type: "text"; text: string } =>
						part.type === "text" && typeof part.text === "string"
				)
				.map((part) => part.text)
				.join("\n\n")
		: "";
	const copyAnswer = async () => {
		if (!(await copyToClipboard(answerText))) return;
		setCopiedAnswer(true);
		window.setTimeout(() => setCopiedAnswer(false), 1500);
	};
	const downloadAnswer = () => {
		const url = URL.createObjectURL(
			new Blob([answerText], { type: "text/markdown;charset=utf-8" })
		);
		const anchor = document.createElement("a");
		anchor.href = url;
		anchor.download = "response.md";
		anchor.click();
		URL.revokeObjectURL(url);
	};

	return (
		<ActionBarPrimitive.Root
			hideWhenRunning
			autohide="not-last"
			autohideFloat="single-branch"
			className="aui-assistant-action-bar-root -ml-1 col-start-3 row-start-2 flex gap-1 text-muted-foreground md:data-floating:absolute md:data-floating:rounded-md md:data-floating:p-1 [&>button]:opacity-100 md:[&>button]:opacity-[var(--aui-button-opacity,1)]"
		>
			<TooltipIconButton tooltip="Copy answer" onClick={copyAnswer} disabled={!answerText}>
				{copiedAnswer ? <CheckIcon /> : <CopyIcon />}
			</TooltipIconButton>
			<TooltipIconButton
				tooltip="Download answer as Markdown"
				onClick={downloadAnswer}
				disabled={!answerText}
			>
				<DownloadIcon />
			</TooltipIconButton>
			<SaveToNoteButton markdown={answerText} />
			{isLast && (
				<ActionBarPrimitive.Reload asChild>
					<TooltipIconButton tooltip="Regenerate response">
						<RefreshCwIcon />
					</TooltipIconButton>
				</ActionBarPrimitive.Reload>
			)}
			{isQuickAssist && (
				<TooltipIconButton
					tooltip="Paste back into source app"
					onClick={() => {
						api?.replaceText(answerText);
					}}
				>
					<ClipboardPaste />
				</TooltipIconButton>
			)}
			<MessageInfoDropdown chatTurnId={chatTurnId} />
		</ActionBarPrimitive.Root>
	);
};
