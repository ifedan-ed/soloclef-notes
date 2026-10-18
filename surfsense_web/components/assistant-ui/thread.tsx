import {
	AuiIf,
	ComposerPrimitive,
	MessagePrimitive,
	ThreadPrimitive,
	useAui,
	useAuiState,
} from "@assistant-ui/react";
import { useAtom, useAtomValue, useSetAtom } from "jotai";
import {
	AlertCircle,
	ArrowUpIcon,
	Camera,
	ChevronDown,
	Clipboard,
	LayoutGrid,
	Plus,
	Settings2,
	SquareIcon,
	Unplug,
	Upload,
	Wrench,
	X,
} from "lucide-react";
import Image from "next/image";
import { useParams, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { type FC, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	agentToolsAtom,
	disabledToolsAtom,
	hydrateDisabledToolsAtom,
	toggleToolAtom,
} from "@/atoms/agent-tools/agent-tools.atoms";
import { chatSessionStateAtom } from "@/atoms/chat/chat-session-state.atom";
import {
	type MentionedDocumentInfo,
	mentionedDocumentsAtom,
	submittedMentionsAtom,
} from "@/atoms/chat/mentioned-documents.atom";
import { pendingUserImageDataUrlsAtom } from "@/atoms/chat/pending-user-images.atom";
import { submittedRetrievalScopeAtom } from "@/atoms/chat/retrieval-scope.atom";
import { importConnectorRequestAtom } from "@/atoms/connector-dialog/connector-dialog.atoms";
import { connectorsAtom } from "@/atoms/connectors/connector-query.atoms";
import { membersAtom } from "@/atoms/members/members-query.atoms";
import { llmSetupStatusAtomFamily } from "@/atoms/model-connections/model-connections-query.atoms";
import { currentUserAtom } from "@/atoms/user/user-query.atoms";
import { AssistantMessage } from "@/components/assistant-ui/assistant-message";
import { ChatSessionStatus } from "@/components/assistant-ui/chat-session-status";
import { ChatViewport } from "@/components/assistant-ui/chat-viewport";
import { ComposerAddMenuDrawer } from "@/components/assistant-ui/composer-add-menu-drawer";
import {
	type ConnectorRow,
	useConnectorRows,
} from "@/components/assistant-ui/connector-popup/hooks/use-connector-rows";
import { ConnectorRowStatus } from "@/components/assistant-ui/connector-popup/your-connectors/connector-row-status";
import { useDocumentUploadDialog } from "@/components/assistant-ui/document-upload-popup";
import {
	InlineMentionEditor,
	type InlineMentionEditorRef,
	type MentionedDocument,
	type SuggestionAnchorRect,
	type SuggestionTriggerInfo,
} from "@/components/assistant-ui/inline-mention-editor";
import { useRetrievalScope } from "@/components/assistant-ui/retrieval-scope-provider";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { UserMessage } from "@/components/assistant-ui/user-message";
import { ChatExamplePrompts } from "@/components/new-chat/chat-example-prompts";
import { ChatHeader } from "@/components/new-chat/chat-header";
import { ComposerSuggestionPopoverContent } from "@/components/new-chat/composer-suggestion-popup";
import { PromptPicker, type PromptPickerRef } from "@/components/new-chat/prompt-picker";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuPortal,
	DropdownMenuSeparator,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Popover, PopoverAnchor } from "@/components/ui/popover";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { getConnectorIcon } from "@/contracts/enums/connectorIcons";
import {
	CONNECTOR_ICON_TO_TYPES,
	CONNECTOR_TOOL_ICON_PATHS,
	getToolDisplayName,
	getToolIcon,
} from "@/contracts/enums/toolIcons";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { scopeForMentionKinds } from "@/contracts/types/retrieval-scope.types";
import {
	isStructuredQuestionInterrupt,
	StructuredQuestionPrompt,
	usePendingInterrupt,
} from "@/features/chat-messages/hitl";
import { useTurnStopping } from "@/features/chat-thread/server-turn/turn-stopping-context";
import { draftToRestore, failedSendDraftAtom } from "@/features/composer-tools/failed-send-draft";
import {
	preferredToolAtom,
	submittedPreferredToolAtom,
} from "@/features/composer-tools/preferred-tool.atom";
import { ComposerToolsMenu } from "@/features/composer-tools/ui/composer-tools-menu";
import { PreferredToolChip } from "@/features/composer-tools/ui/preferred-tool-chip";
import { useConnectedKeyedTools } from "@/features/composer-tools/use-connected-keyed-tools";
import { useNotebookChat } from "@/features/notebooks/state/notebook-chat-context";
import { NotebookAskBridge } from "@/features/notebooks/ui/chat/notebook-ask-bridge";
import { NotebookSourcePicker } from "@/features/notebooks/ui/chat/notebook-source-picker";
import { NotebookWelcome } from "@/features/notebooks/ui/chat/notebook-welcome";
import { NotebookSourcesChip } from "@/features/notebooks/ui/notebook-sources-chip";
import { InstructionsAppliedBadge } from "@/features/workspace-instructions";
import { useBatchCommentsPreload } from "@/hooks/use-comments";
import { useCommentsSync } from "@/hooks/use-comments-sync";
import { useMediaQuery } from "@/hooks/use-media-query";
import { useElectronAPI } from "@/hooks/use-platform";
import { canSubmitChat } from "@/lib/chat/can-submit-chat";
import { captureDisplayToPngDataUrl } from "@/lib/chat/display-media-capture";
import { getMentionDocKey } from "@/lib/chat/mention-doc-key";
import { slideoutOpenedTickAtom } from "@/lib/layout-events";
import { getWorkspaceIdNumber } from "@/lib/route-params";
import { cn } from "@/lib/utils";
import type { ComposerSuggestionNavigatorRef } from "../new-chat/use-composer-suggestion-navigator";
import { ThreadMessagesSkeletonBody } from "./thread-messages-skeleton";

type ComposerSuggestionAnchorPoint = {
	left: number;
	top: number;
};

function ComposerSuggestionAnchor({ point }: { point: ComposerSuggestionAnchorPoint }) {
	return (
		<PopoverAnchor
			className="pointer-events-none fixed size-0"
			style={{
				left: point.left,
				top: point.top,
			}}
		/>
	);
}

function getComposerSuggestionAnchorPoint(
	triggerRect: SuggestionAnchorRect | null,
	side: "top" | "bottom"
): ComposerSuggestionAnchorPoint | null {
	if (!triggerRect) return null;
	return {
		left: triggerRect.left,
		top: side === "bottom" ? triggerRect.bottom : triggerRect.top,
	};
}

interface ThreadProps {
	hasActiveThread?: boolean;
	isLoadingMessages?: boolean;
}

export const Thread: FC<ThreadProps> = ({ hasActiveThread = false, isLoadingMessages = false }) => {
	return <ThreadContent hasActiveThread={hasActiveThread} isLoadingMessages={isLoadingMessages} />;
};

const ThreadContent: FC<ThreadProps> = ({
	hasActiveThread: hasThread = false,
	isLoadingMessages = false,
}) => {
	// A notebook chat always exists, but while empty it shows the notebook's
	// own welcome (with the composer inside it), like a brand-new chat does.
	const inNotebook = useNotebookChat() !== null;
	const hasActiveThread = hasThread && !inNotebook;
	return (
		<ThreadPrimitive.Root
			className="aui-root aui-thread-root @container relative flex h-full min-h-0 min-w-0 w-full flex-col bg-main-panel"
			style={{
				["--thread-max-width" as string]: "42rem",
			}}
		>
			{inNotebook ? <NotebookAskBridge /> : null}
			<ChatViewport
				footerAlwaysVisible={hasActiveThread}
				footer={
					<>
						<StructuredQuestionPrompt />
						<Composer isLoadingMessages={isLoadingMessages} />
					</>
				}
			>
				<AuiIf
					condition={({ thread }) =>
						!hasActiveThread && thread.isEmpty && !(inNotebook && isLoadingMessages)
					}
				>
					<ThreadWelcome />
				</AuiIf>

				{isLoadingMessages ? <ThreadMessagesSkeletonBody /> : null}

				<ThreadPrimitive.Messages
					components={{
						UserMessage,
						EditComposer,
						AssistantMessage,
					}}
				/>
			</ChatViewport>
		</ThreadPrimitive.Root>
	);
};

const getTimeBasedGreeting = (user?: { display_name?: string | null; email?: string }): string => {
	const hour = new Date().getHours();

	let firstName: string | null = null;
	if (user?.display_name?.trim()) {
		const nameParts = user.display_name.trim().split(/\s+/);
		firstName = nameParts[0].charAt(0).toUpperCase() + nameParts[0].slice(1).toLowerCase();
	} else if (user?.email) {
		firstName =
			user.email.split("@")[0].split(".")[0].charAt(0).toUpperCase() +
			user.email.split("@")[0].split(".")[0].slice(1);
	}

	const morningGreetings = ["Good morning", "Fresh start today", "Morning", "Hey there"];
	const afternoonGreetings = ["Good afternoon", "Afternoon", "Hey there", "Hi there"];
	const eveningGreetings = ["Good evening", "Evening", "Hey there", "Hi there"];
	const nightGreetings = ["Good night", "Evening", "Hey there", "Winding down"];
	const lateNightGreetings = ["Still up", "Night owl mode", "Up past bedtime", "Hi there"];

	let greeting: string;
	if (hour < 5) {
		greeting = lateNightGreetings[Math.floor(Math.random() * lateNightGreetings.length)];
	} else if (hour < 12) {
		greeting = morningGreetings[Math.floor(Math.random() * morningGreetings.length)];
	} else if (hour < 18) {
		greeting = afternoonGreetings[Math.floor(Math.random() * afternoonGreetings.length)];
	} else if (hour < 22) {
		greeting = eveningGreetings[Math.floor(Math.random() * eveningGreetings.length)];
	} else {
		greeting = nightGreetings[Math.floor(Math.random() * nightGreetings.length)];
	}

	return firstName ? `${greeting}, ${firstName}!` : `${greeting}!`;
};

const ThreadWelcome: FC = () => {
	const { data: user } = useAtomValue(currentUserAtom);
	const greeting = useMemo(() => getTimeBasedGreeting(user), [user]);
	const notebook = useNotebookChat();

	if (notebook) return <NotebookWelcome composer={<Composer />} />;

	return (
		<div className="aui-thread-welcome-root flex min-h-0 min-w-0 w-full flex-1">
			<section className="mx-auto grid h-full min-w-0 w-full max-w-(--thread-max-width) grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)_auto_minmax(0,1fr)] pt-8 pb-[clamp(5rem,16vh,12rem)]">
				<div className="aui-thread-welcome-message flex flex-col items-center self-end px-4 pb-6 text-center">
					<h1 className="aui-thread-welcome-message-inner min-w-0 w-full max-w-full break-words text-balance text-3xl select-none md:text-[2.625rem]">
						{greeting}
					</h1>
				</div>
				<div className="flex w-full items-start justify-center">
					<Composer showExamplePrompts />
				</div>
			</section>
		</div>
	);
};

const PendingScreenImageStrip: FC = () => {
	const [urls, setUrls] = useAtom(pendingUserImageDataUrlsAtom);
	if (urls.length === 0) return null;
	return (
		<div className="mx-3 mt-2 flex flex-wrap gap-2">
			{urls.map((url, index) => (
				<div
					key={url}
					className="group relative h-14 w-14 shrink-0 overflow-hidden rounded-md border border-border/50 bg-muted"
				>
					<Image
						src={url}
						alt="Pending screenshot preview"
						fill
						sizes="56px"
						className="object-cover"
						draggable={false}
						unoptimized
					/>
					<Button
						type="button"
						onClick={() => setUrls((prev) => prev.filter((_, i) => i !== index))}
						variant="ghost"
						size="icon"
						className="absolute right-0.5 top-0.5 size-5 rounded-full bg-background/90 text-muted-foreground shadow-sm transition-opacity hover:bg-background/90 hover:text-accent-foreground sm:opacity-0 sm:group-hover:opacity-100"
						aria-label="Remove screenshot"
					>
						<X className="size-3" />
					</Button>
				</div>
			))}
		</div>
	);
};

const ClipboardChip: FC<{ text: string; onDismiss: () => void }> = ({ text, onDismiss }) => {
	const [expanded, setExpanded] = useState(false);
	const isLong = text.length > 120;
	const preview = isLong ? `${text.slice(0, 120)}…` : text;

	return (
		<div className="mx-3 mt-2 rounded-lg border border-border/40 bg-background/60">
			<div className="flex items-center gap-2 px-3 py-2">
				<Clipboard className="size-4 shrink-0 text-muted-foreground" />
				<span className="text-xs font-medium text-muted-foreground">From clipboard</span>
				<div className="flex-1" />
				{isLong && (
					<Button
						type="button"
						onClick={() => setExpanded((v) => !v)}
						variant="ghost"
						size="icon"
						className="size-5 text-muted-foreground hover:bg-transparent hover:text-accent-foreground"
					>
						<ChevronDown
							className={cn("size-3.5 transition-transform", expanded && "rotate-180")}
						/>
					</Button>
				)}
				<Button
					type="button"
					onClick={onDismiss}
					variant="ghost"
					size="icon"
					className="size-5 text-muted-foreground hover:bg-transparent hover:text-accent-foreground"
				>
					<X className="size-3.5" />
				</Button>
			</div>
			<div className="px-3 pb-2">
				<p className="text-xs text-foreground/80 whitespace-pre-wrap wrap-break-word leading-relaxed">
					{expanded ? text : preview}
				</p>
			</div>
		</div>
	);
};

/**
 * Inline "this workspace can't chat right now" tray, mirrored on top of the
 * composer. Chat runs on the shared models every account has, so there is
 * nothing for a member to connect; this only shows if that list is gone.
 * The send button stays disabled independently, and the backend rejects any
 * send that slips through, so this is purely guidance.
 *
 * Presentational: visibility is decided by the parent so the composer can flatten
 * its top shadow for a seamless join.
 */
const ChatUnavailableNotice: FC = () => (
	<div className="relative z-0 -mb-5 flex min-w-0 items-center gap-2 rounded-t-3xl bg-popover px-4 pt-2 pb-6 shadow-sm shadow-black/5 dark:shadow-black/10">
		<div className="flex min-w-0 items-center gap-2 text-[13px] font-normal text-muted-foreground select-none">
			<AlertCircle className="size-4 shrink-0" />
			<span className="truncate">Chat is unavailable right now. Please try again shortly.</span>
		</div>
	</div>
);

interface ComposerProps {
	isLoadingMessages?: boolean;
	showExamplePrompts?: boolean;
}

const Composer: FC<ComposerProps> = ({ isLoadingMessages = false, showExamplePrompts = false }) => {
	const notebookChat = useNotebookChat();
	const [mentionedDocuments, setMentionedDocuments] = useAtom(mentionedDocumentsAtom);
	const setSubmittedMentions = useSetAtom(submittedMentionsAtom);
	const { scope: retrievalScope, setScope: setRetrievalScope } = useRetrievalScope();
	const setSubmittedRetrievalScope = useSetAtom(submittedRetrievalScopeAtom);
	const [preferredTool, setPreferredTool] = useAtom(preferredToolAtom);
	const setSubmittedPreferredTool = useSetAtom(submittedPreferredToolAtom);
	const [showDocumentPopover, setShowDocumentPopover] = useState(false);
	const [showPromptPicker, setShowPromptPicker] = useState(false);
	const [mentionQuery, setMentionQuery] = useState("");
	const [actionQuery, setActionQuery] = useState("");
	const [suggestionAnchorPoint, setSuggestionAnchorPoint] =
		useState<ComposerSuggestionAnchorPoint | null>(null);
	const [isComposerInputEmpty, setIsComposerInputEmpty] = useState(true);
	const editorRef = useRef<InlineMentionEditorRef>(null);
	const inputWrapperRef = useRef<HTMLDivElement>(null);
	const placeholderMeasureRef = useRef<HTMLSpanElement>(null);
	const prevMentionedDocsRef = useRef<Map<string, MentionedDocumentInfo>>(new Map());
	const documentPickerRef = useRef<ComposerSuggestionNavigatorRef>(null);
	const promptPickerRef = useRef<PromptPickerRef>(null);
	const params = useParams();
	const workspaceId = getWorkspaceIdNumber(params);
	const chat_id = params.chat_id;
	const aui = useAui();
	const [failedSend, setFailedSend] = useAtom(failedSendDraftAtom);

	// A send that failed before the server took it: put its text and tool back.
	useEffect(() => {
		if (!failedSend) return;
		const restore = draftToRestore(
			{ isEmpty: isComposerInputEmpty, tool: preferredTool },
			failedSend
		);
		setFailedSend(null);
		if (restore.text) {
			editorRef.current?.setText(restore.text);
			aui.composer().setText(restore.text);
			setIsComposerInputEmpty(false);
		}
		if (restore.tool !== preferredTool) setPreferredTool(restore.tool);
	}, [failedSend, setFailedSend, isComposerInputEmpty, preferredTool, setPreferredTool, aui]);
	// A composer that mounts over a thread that already has a draft (the
	// notebook welcome swaps its own composer in once the chat's messages have
	// loaded) starts from that draft, so what was typed during the swap stays.
	const draftText = useAuiState(({ composer }) => composer.text ?? "");
	const draftTextRef = useRef(draftText);
	draftTextRef.current = draftText;
	useEffect(() => {
		const draft = draftTextRef.current;
		if (!draft.trim() || editorRef.current?.getText()) return;
		editorRef.current?.setText(draft);
		setIsComposerInputEmpty(false);
	}, []);
	const pendingInterrupts = usePendingInterrupt()?.pendingInterrupts ?? [];
	const isPausedForQuestion = pendingInterrupts.some(isStructuredQuestionInterrupt);
	// Desktop-only auto-focus; on mobile, programmatic focus would
	// summon the soft keyboard on every picker close / thread switch.
	const isDesktop = useMediaQuery("(min-width: 640px)");

	const electronAPI = useElectronAPI();
	const [clipboardInitialText, setClipboardInitialText] = useState<string | undefined>();
	const clipboardLoadedRef = useRef(false);
	useEffect(() => {
		if (!electronAPI || clipboardLoadedRef.current) return;
		clipboardLoadedRef.current = true;
		electronAPI.getQuickAskText().then((text: string) => {
			if (text) {
				setClipboardInitialText(text);
			}
		});
	}, [electronAPI]);

	const isThreadEmpty = useAuiState(({ thread }) => thread.isEmpty);
	const isThreadRunning = useAuiState(({ thread }) => thread.isRunning);

	const { data: chatSetupStatus } = useAtomValue(llmSetupStatusAtomFamily(workspaceId ?? 0));
	const isChatUnavailable = !!chatSetupStatus && chatSetupStatus.status !== "ready";

	const [longPlaceholderFits, setLongPlaceholderFits] = useState(false);
	useEffect(() => {
		if (!isDesktop) {
			setLongPlaceholderFits(false);
			return;
		}

		const wrapper = inputWrapperRef.current;
		const measure = placeholderMeasureRef.current;
		const editor = wrapper?.querySelector<HTMLElement>('[contenteditable="true"]');
		if (!editor || !measure) return;

		const update = () => setLongPlaceholderFits(measure.scrollWidth <= editor.clientWidth);
		update();

		const observer = new ResizeObserver(update);
		observer.observe(editor);
		observer.observe(measure);
		return () => observer.disconnect();
	}, [isDesktop]);

	const tComposer = useTranslations("composer_tools");
	const placeholderLong = tComposer("placeholder_long");
	const currentPlaceholder =
		isDesktop && longPlaceholderFits ? placeholderLong : tComposer("placeholder_short");

	const { data: currentUser } = useAtomValue(currentUserAtom);
	const { data: members } = useAtomValue(membersAtom);
	const threadId = useMemo(() => {
		if (Array.isArray(chat_id) && chat_id.length > 0) {
			return Number.parseInt(chat_id[0], 10) || null;
		}
		return typeof chat_id === "string" ? Number.parseInt(chat_id, 10) || null : null;
	}, [chat_id]);
	const sessionState = useAtomValue(chatSessionStateAtom);
	const isAiResponding = sessionState?.isAiResponding ?? false;
	const respondingToUserId = sessionState?.respondingToUserId ?? null;
	const isBlockedByOtherUser = isAiResponding && respondingToUserId !== currentUser?.id;

	// One Zero subscription per thread for comment sync.
	useCommentsSync(threadId);

	// Batch-prefetch assistant message comments to avoid N+1 fetches.
	// Returns a primitive string so useSyncExternalStore can compare by value.
	const assistantIdsKey = useAuiState(({ thread }) =>
		thread.messages
			.filter((m) => m.role === "assistant" && m.id?.startsWith("msg-"))
			.map((m) => m.id?.replace("msg-", ""))
			.join(",")
	);
	const assistantDbMessageIds = useMemo(
		() => (assistantIdsKey ? assistantIdsKey.split(",").map(Number) : []),
		[assistantIdsKey]
	);
	useBatchCommentsPreload(assistantDbMessageIds);

	// Always-focused composer: refocus whenever no picker has taken
	// over input. ``threadId`` is in the deps so the effect re-fires
	// on thread switch (Composer instance is reused).
	useEffect(() => {
		if (!isDesktop) return;
		if (showDocumentPopover || showPromptPicker) return;
		void threadId;
		editorRef.current?.focus();
	}, [isDesktop, showDocumentPopover, showPromptPicker, threadId]);

	const handleChatModelSelected = useCallback(() => {
		if (!isDesktop) return;
		editorRef.current?.focus();
	}, [isDesktop]);

	// Close document picker when a sidebar slide-out panel (inbox, etc.) opens.
	// React only on changes to the tick — comparing against the previously-seen
	// value preserves the one-shot semantics of the prior window-event approach
	// (no retroactive close on mount if a panel had already opened earlier).
	const slideoutOpenedTick = useAtomValue(slideoutOpenedTickAtom);
	const lastSeenSlideoutTickRef = useRef(slideoutOpenedTick);
	useEffect(() => {
		if (lastSeenSlideoutTickRef.current === slideoutOpenedTick) return;
		lastSeenSlideoutTickRef.current = slideoutOpenedTick;
		setShowDocumentPopover(false);
		setMentionQuery("");
		setSuggestionAnchorPoint(null);
	}, [slideoutOpenedTick]);

	// Sync editor text into assistant-ui's composer and mirror the chip
	// atom from the editor's reported ``docs``. The editor is the
	// single source of truth, so this catches every Plate deletion path
	// (Backspace, X button, Cmd+Backspace, range-delete, cut,
	// paste-over) without per-keybinding plumbing. The ``prev``
	// short-circuit keeps pure-text keystrokes from churning the atom.
	const handleEditorChange = useCallback(
		(text: string, docs: MentionedDocument[]) => {
			setIsComposerInputEmpty(text.trim().length === 0 && docs.length === 0);
			aui.composer().setText(text);
			setMentionedDocuments((prev) => {
				if (prev.length === docs.length) {
					const editorKeys = new Set(docs.map((d) => getMentionDocKey(d)));
					if (prev.every((d) => editorKeys.has(getMentionDocKey(d)))) {
						return prev;
					}
				}
				return docs.map<MentionedDocumentInfo>((d) => {
					if (d.kind === "connector") {
						return {
							id: d.id,
							title: d.title,
							kind: "connector",
							connector_type: d.connector_type ?? "UNKNOWN",
							account_name: d.account_name ?? d.title,
						};
					}
					if (d.kind === "folder") {
						return {
							id: d.id,
							title: d.title,
							kind: "folder",
						};
					}
					if (d.kind === "thread") {
						return {
							id: d.id,
							title: d.title,
							kind: "thread",
						};
					}
					return {
						id: d.id,
						title: d.title,
						document_type: d.document_type ?? "UNKNOWN",
						kind: "doc",
					};
				});
			});
		},
		[aui, setMentionedDocuments]
	);

	const handleMentionTrigger = useCallback((trigger: SuggestionTriggerInfo) => {
		const anchorPoint = getComposerSuggestionAnchorPoint(trigger.anchorRect, "top");
		if (!anchorPoint) {
			setShowDocumentPopover(false);
			setMentionQuery("");
			setSuggestionAnchorPoint(null);
			return;
		}
		setSuggestionAnchorPoint((current) => current ?? anchorPoint);
		setShowDocumentPopover(true);
		setMentionQuery(trigger.query);
	}, []);

	const handleMentionClose = useCallback(() => {
		if (showDocumentPopover) {
			setShowDocumentPopover(false);
			setMentionQuery("");
			setSuggestionAnchorPoint(null);
		}
	}, [showDocumentPopover]);

	const handleDocumentPopoverOpenChange = useCallback((open: boolean) => {
		setShowDocumentPopover(open);
		if (!open) {
			setMentionQuery("");
			setSuggestionAnchorPoint(null);
		}
	}, []);

	const handleActionTrigger = useCallback(
		(trigger: SuggestionTriggerInfo) => {
			const anchorPoint = getComposerSuggestionAnchorPoint(
				trigger.anchorRect,
				clipboardInitialText ? "bottom" : "top"
			);
			if (!anchorPoint) {
				setShowPromptPicker(false);
				setActionQuery("");
				setSuggestionAnchorPoint(null);
				return;
			}
			setSuggestionAnchorPoint((current) => current ?? anchorPoint);
			setShowPromptPicker(true);
			setActionQuery(trigger.query);
		},
		[clipboardInitialText]
	);

	const handleActionClose = useCallback(() => {
		if (showPromptPicker) {
			setShowPromptPicker(false);
			setActionQuery("");
			setSuggestionAnchorPoint(null);
		}
	}, [showPromptPicker]);

	const handlePromptPickerOpenChange = useCallback((open: boolean) => {
		setShowPromptPicker(open);
		if (!open) {
			setActionQuery("");
			setSuggestionAnchorPoint(null);
		}
	}, []);

	const handleActionSelect = useCallback(
		(action: { name: string; prompt: string; mode: "transform" | "explore" }) => {
			let userText = editorRef.current?.getText() ?? "";
			const trigger = `/${actionQuery}`;
			if (userText.endsWith(trigger)) {
				userText = userText.slice(0, -trigger.length).trimEnd();
			}
			const finalPrompt = action.prompt.includes("{selection}")
				? action.prompt.replace("{selection}", () => userText)
				: userText
					? `${action.prompt}\n\n${userText}`
					: action.prompt;
			editorRef.current?.setText(finalPrompt);
			aui.composer().setText(finalPrompt);
			setIsComposerInputEmpty(false);
			setShowPromptPicker(false);
			setActionQuery("");
			setSuggestionAnchorPoint(null);
		},
		[actionQuery, aui]
	);

	const handleExampleSelect = useCallback(
		(prompt: string) => {
			editorRef.current?.setText(prompt);
			aui.composer().setText(prompt);
			setIsComposerInputEmpty(false);
			editorRef.current?.focus();
		},
		[aui]
	);

	const handleQuickAskSelect = useCallback(
		(action: { name: string; prompt: string; mode: "transform" | "explore" }) => {
			if (!clipboardInitialText) return;
			electronAPI?.setQuickAskMode(action.mode);
			const finalPrompt = action.prompt.includes("{selection}")
				? action.prompt.replace("{selection}", () => clipboardInitialText)
				: `${action.prompt}\n\n${clipboardInitialText}`;
			editorRef.current?.setText(finalPrompt);
			aui.composer().setText(finalPrompt);
			setIsComposerInputEmpty(false);
			setShowPromptPicker(false);
			setActionQuery("");
			setSuggestionAnchorPoint(null);
			setClipboardInitialText(undefined);
		},
		[clipboardInitialText, electronAPI, aui]
	);

	// Arrow / Enter / Escape navigation for the active picker.
	const handleKeyDown = useCallback(
		(e: React.KeyboardEvent) => {
			// While an IME composition is active (e.g. confirming a Japanese/Chinese/
			// Korean conversion), let the Enter/Arrow keys reach the IME instead of
			// driving picker navigation/selection.
			if (e.nativeEvent.isComposing) return;
			if (showPromptPicker) {
				if (e.key === "ArrowDown") {
					e.preventDefault();
					promptPickerRef.current?.moveDown();
					return;
				}
				if (e.key === "ArrowUp") {
					e.preventDefault();
					promptPickerRef.current?.moveUp();
					return;
				}
				if (e.key === "Enter") {
					e.preventDefault();
					promptPickerRef.current?.selectHighlighted();
					return;
				}
				if (e.key === "Escape") {
					e.preventDefault();
					setShowPromptPicker(false);
					setActionQuery("");
					setSuggestionAnchorPoint(null);
					return;
				}
			}
			if (showDocumentPopover) {
				if (e.key === "ArrowDown") {
					e.preventDefault();
					documentPickerRef.current?.moveDown();
					return;
				}
				if (e.key === "ArrowUp") {
					e.preventDefault();
					documentPickerRef.current?.moveUp();
					return;
				}
				if (e.key === "Enter") {
					e.preventDefault();
					documentPickerRef.current?.selectHighlighted();
					return;
				}
				if (e.key === "Escape") {
					e.preventDefault();
					if (documentPickerRef.current?.goBack()) {
						return;
					}
					setShowDocumentPopover(false);
					setMentionQuery("");
					setSuggestionAnchorPoint(null);
					return;
				}
			}
		},
		[showDocumentPopover, showPromptPicker]
	);

	const handleSubmit = useCallback(() => {
		if (isLoadingMessages || isThreadRunning || isBlockedByOtherUser || isPausedForQuestion) return;
		if (showDocumentPopover || showPromptPicker) return;

		if (clipboardInitialText) {
			const userText = editorRef.current?.getText() ?? "";
			const combined = userText ? `${userText}\n\n${clipboardInitialText}` : clipboardInitialText;
			aui.composer().setText(combined);
			setClipboardInitialText(undefined);
		}

		// Capture chips before the reset below clears the live atom, so
		// the async ``onNew`` still sees them.
		setSubmittedMentions(mentionedDocuments);
		const effectiveScope = scopeForMentionKinds(
			retrievalScope,
			mentionedDocuments.map((mention) => mention.kind)
		);
		if (effectiveScope !== retrievalScope) setRetrievalScope(effectiveScope);
		setSubmittedRetrievalScope(effectiveScope);
		setSubmittedPreferredTool(preferredTool);

		aui.composer().send();
		editorRef.current?.clear();
		setIsComposerInputEmpty(true);
		setMentionedDocuments([]);
		setPreferredTool(null);
	}, [
		showDocumentPopover,
		showPromptPicker,
		isLoadingMessages,
		isThreadRunning,
		isBlockedByOtherUser,
		isPausedForQuestion,
		clipboardInitialText,
		aui,
		mentionedDocuments,
		retrievalScope,
		setSubmittedMentions,
		setSubmittedRetrievalScope,
		setRetrievalScope,
		setMentionedDocuments,
		preferredTool,
		setSubmittedPreferredTool,
		setPreferredTool,
	]);

	const handleDocumentRemove = useCallback(
		(
			docId: number,
			docType?: string,
			kind?: "doc" | "folder" | "connector" | "thread",
			connectorType?: string
		) => {
			setMentionedDocuments((prev) => {
				const removedKey = getMentionDocKey({
					id: docId,
					document_type: docType,
					kind,
					connector_type: connectorType,
				});
				return prev.filter((doc) => getMentionDocKey(doc) !== removedKey);
			});
		},
		[setMentionedDocuments]
	);

	const handleDocumentsMention = useCallback(
		(mentions: MentionedDocumentInfo[]) => {
			setRetrievalScope(
				scopeForMentionKinds(
					retrievalScope,
					[...mentionedDocuments, ...mentions].map((mention) => mention.kind)
				)
			);
			const editorMentionedDocs = editorRef.current?.getMentionedDocuments() ?? [];
			const editorDocKeys = new Set(editorMentionedDocs.map((doc) => getMentionDocKey(doc)));

			for (const mention of mentions) {
				const key = getMentionDocKey(mention);
				if (editorDocKeys.has(key)) continue;
				editorRef.current?.insertMentionChip(mention);
				// Track within the loop so a duplicate-in-batch can't double-insert.
				editorDocKeys.add(key);
			}

			// Atom is reconciled by ``handleEditorChange`` via the editor's
			// onChange — no second write path here.
			setMentionQuery("");
			setSuggestionAnchorPoint(null);
		},
		[mentionedDocuments, retrievalScope, setRetrievalScope]
	);

	useEffect(() => {
		const effectiveScope = scopeForMentionKinds(
			retrievalScope,
			mentionedDocuments.map((mention) => mention.kind)
		);
		if (effectiveScope !== retrievalScope) setRetrievalScope(effectiveScope);

		const editor = editorRef.current;
		const nextDocsMap = new Map(mentionedDocuments.map((doc) => [getMentionDocKey(doc), doc]));
		const prevDocsMap = prevMentionedDocsRef.current;

		if (!editor) {
			prevMentionedDocsRef.current = nextDocsMap;
			return;
		}

		const editorKeys = new Set(editor.getMentionedDocuments().map(getMentionDocKey));

		for (const [key, doc] of nextDocsMap) {
			if (prevDocsMap.has(key) || editorKeys.has(key)) continue;
			editor.insertMentionChip(doc, { removeTriggerText: false });
		}

		for (const [key, doc] of prevDocsMap) {
			if (!nextDocsMap.has(key)) {
				editor.removeDocumentChip(
					doc.id,
					doc.kind === "doc" ? doc.document_type : undefined,
					doc.kind,
					doc.kind === "connector" ? doc.connector_type : undefined
				);
			}
		}

		prevMentionedDocsRef.current = nextDocsMap;
	}, [mentionedDocuments, retrievalScope, setRetrievalScope]);

	return (
		<ComposerPrimitive.Root className="aui-composer-root relative flex min-w-0 w-full flex-col gap-2 rounded-2xl">
			<ChatSessionStatus
				isAiResponding={isAiResponding}
				respondingToUserId={respondingToUserId}
				currentUserId={currentUser?.id ?? null}
				members={members ?? []}
			/>
			<Popover open={showDocumentPopover} onOpenChange={handleDocumentPopoverOpenChange}>
				{suggestionAnchorPoint && notebookChat ? (
					<>
						<ComposerSuggestionAnchor point={suggestionAnchorPoint} />
						<ComposerSuggestionPopoverContent side="top">
							<NotebookSourcePicker
								ref={documentPickerRef}
								workspaceId={notebookChat.workspaceId}
								notebookId={notebookChat.notebookId}
								onSelectionChange={handleDocumentsMention}
								onDone={() => {
									setShowDocumentPopover(false);
									setMentionQuery("");
									setSuggestionAnchorPoint(null);
								}}
								initialSelectedDocuments={mentionedDocuments}
								externalSearch={mentionQuery}
							/>
						</ComposerSuggestionPopoverContent>
					</>
				) : null}
			</Popover>
			<Popover open={showPromptPicker} onOpenChange={handlePromptPickerOpenChange}>
				{suggestionAnchorPoint ? (
					<>
						<ComposerSuggestionAnchor point={suggestionAnchorPoint} />
						<ComposerSuggestionPopoverContent side={clipboardInitialText ? "bottom" : "top"}>
							<PromptPicker
								ref={promptPickerRef}
								onSelect={clipboardInitialText ? handleQuickAskSelect : handleActionSelect}
								onDone={() => {
									setShowPromptPicker(false);
									setActionQuery("");
									setSuggestionAnchorPoint(null);
								}}
								externalSearch={actionQuery}
							/>
						</ComposerSuggestionPopoverContent>
					</>
				) : null}
			</Popover>
			<div className="relative flex min-w-0 w-full flex-col">
				{isChatUnavailable ? <ChatUnavailableNotice /> : null}
				<div
					className={cn(
						"aui-composer-attachment-dropzone relative z-10 flex min-w-0 w-full flex-col overflow-hidden rounded-3xl border border-input/20 bg-muted pt-2 shadow-sm shadow-black/5 outline-none transition-[border-color,box-shadow] hover:border-input focus-within:border-input dark:shadow-black/10",
						isChatUnavailable && "shadow-none dark:shadow-none"
					)}
				>
					<PendingScreenImageStrip />
					{clipboardInitialText && (
						<ClipboardChip
							text={clipboardInitialText}
							onDismiss={() => setClipboardInitialText(undefined)}
						/>
					)}
					<PreferredToolChip tool={preferredTool} onRemove={() => setPreferredTool(null)} />
					<div
						ref={inputWrapperRef}
						className="aui-composer-input-wrapper relative px-4 py-2 sm:mb-5"
					>
						<span
							ref={placeholderMeasureRef}
							aria-hidden="true"
							className="invisible absolute text-sm leading-6 font-normal whitespace-nowrap"
						>
							{placeholderLong}
						</span>
						<InlineMentionEditor
							ref={editorRef}
							placeholder={currentPlaceholder}
							onMentionTrigger={handleMentionTrigger}
							onMentionClose={handleMentionClose}
							onActionTrigger={handleActionTrigger}
							onActionClose={handleActionClose}
							onChange={handleEditorChange}
							onDocumentRemove={handleDocumentRemove}
							onSubmit={handleSubmit}
							onKeyDown={handleKeyDown}
							disabled={isPausedForQuestion}
							className="min-h-[24px] **:data-slate-placeholder:font-normal **:data-slate-placeholder:whitespace-nowrap"
						/>
					</div>
					<ComposerAction
						onSend={handleSubmit}
						isBlockedByOtherUser={isBlockedByOtherUser}
						isLoadingMessages={isLoadingMessages}
						isThreadRunning={isThreadRunning || isPausedForQuestion}
						workspaceId={workspaceId ?? 0}
						onChatModelSelected={handleChatModelSelected}
					/>
				</div>
				{showExamplePrompts && !isLoadingMessages && isThreadEmpty && isComposerInputEmpty ? (
					<div className="absolute top-full left-0 right-0 z-20">
						<ChatExamplePrompts onSelect={handleExampleSelect} />
					</div>
				) : null}
			</div>
		</ComposerPrimitive.Root>
	);
};

interface ComposerActionProps {
	isBlockedByOtherUser?: boolean;
	isLoadingMessages?: boolean;
	isThreadRunning?: boolean;
	workspaceId: number;
	onChatModelSelected?: () => void;
	/**
	 * Must be the same handler the editor's Enter key uses. Going through
	 * ``ComposerPrimitive.Send`` instead would send without the composer's
	 * mention snapshot and editor reset.
	 */
	onSend: () => void;
}

const ComposerAction: FC<ComposerActionProps> = ({
	isBlockedByOtherUser = false,
	isLoadingMessages = false,
	isThreadRunning = false,
	workspaceId,
	onChatModelSelected,
	onSend,
}) => {
	const mentionedDocuments = useAtomValue(mentionedDocumentsAtom);
	const router = useRouter();
	const openConnectors = useCallback(
		() => router.push(`/dashboard/${workspaceId}/workspace-settings/connectors`),
		[router, workspaceId]
	);
	const [toolsPopoverOpen, setToolsPopoverOpen] = useState(false);
	const [openConnectorSubmenu, setOpenConnectorSubmenu] = useState<string | null>(null);
	const [preferredTool, setPreferredTool] = useAtom(preferredToolAtom);
	const isDesktop = useMediaQuery("(min-width: 640px)");
	const { openDialog: openWorkspaceUploadDialog } = useDocumentUploadDialog();
	// In a notebook chat an upload becomes a notebook source (the only files
	// that chat can read); elsewhere it goes to the workspace knowledge base.
	const notebookChat = useNotebookChat();
	const notebookUpload = notebookChat?.uploadSources;
	// Exa, Tavily and Browserbase are offered where the workspace has their key.
	const connectedKeyedTools = useConnectedKeyedTools(workspaceId, notebookChat?.notebookId);
	const openUploadDialog = notebookUpload ?? openWorkspaceUploadDialog;
	const pendingScreenImages = useAtomValue(pendingUserImageDataUrlsAtom);
	const setPendingScreenImages = useSetAtom(pendingUserImageDataUrlsAtom);
	const electronAPI = useElectronAPI();
	const turnStopping = useTurnStopping();

	const isComposerTextEmpty = useAuiState(({ composer }) => {
		const text = composer.text?.trim() || "";
		return text.length === 0;
	});
	const isComposerEmpty =
		isComposerTextEmpty && mentionedDocuments.length === 0 && pendingScreenImages.length === 0;

	const handleScreenCapture = useCallback(async () => {
		const url = electronAPI?.captureFullScreen
			? await electronAPI.captureFullScreen()
			: await captureDisplayToPngDataUrl();
		if (url) setPendingScreenImages((prev) => [...prev, url]);
	}, [electronAPI, setPendingScreenImages]);

	const { data: setupStatus } = useAtomValue(llmSetupStatusAtomFamily(workspaceId));

	const { data: agentTools } = useAtomValue(agentToolsAtom);
	const disabledTools = useAtomValue(disabledToolsAtom);
	const disabledToolsSet = useMemo(() => new Set(disabledTools), [disabledTools]);
	const toggleTool = useSetAtom(toggleToolAtom);
	const setDisabledTools = useSetAtom(disabledToolsAtom);
	const hydrateDisabled = useSetAtom(hydrateDisabledToolsAtom);

	const { data: connectors } = useAtomValue(connectorsAtom);
	const connectedTypes = useMemo(
		() => new Set<string>((connectors ?? []).map((c) => c.connector_type)),
		[connectors]
	);
	// "Your connectors" rows for the add-menu (desktop submenu + mobile drawer):
	// same hook the connectors panel rail uses, so grouping + health glyphs match.
	const { rows: connectorRows } = useConnectorRows((connectors ?? []) as SearchSourceConnector[]);
	const setImportRequest = useSetAtom(importConnectorRequestAtom);
	const openConnectorManage = useCallback(
		(row: ConnectorRow) => {
			// Deep-link into the connector's manage view: the panel's
			// useConnectorDialog consumes this atom and routes by account count
			// (none -> connect, one -> edit, many -> accounts list).
			setImportRequest({ connectorType: row.type, mode: "auto" });
			openConnectors();
		},
		[setImportRequest, openConnectors]
	);

	const toggleToolGroup = useCallback(
		(toolNames: string[]) => {
			const allDisabled = toolNames.every((name) => disabledToolsSet.has(name));
			if (allDisabled) {
				setDisabledTools((prev) => prev.filter((t) => !toolNames.includes(t)));
			} else {
				setDisabledTools((prev) => [...new Set([...prev, ...toolNames])]);
			}
		},
		[disabledToolsSet, setDisabledTools]
	);
	const filteredTools = agentTools;
	const groupedTools = useMemo(() => {
		if (!filteredTools) return [];
		const toolsByName = new Map(filteredTools.map((t) => [t.name, t]));
		const result: { label: string; tools: typeof filteredTools; connectorIcon?: string }[] = [];
		const placed = new Set<string>();

		for (const group of TOOL_GROUPS) {
			if (group.connectorIcon) {
				const requiredTypes = CONNECTOR_ICON_TO_TYPES[group.connectorIcon];
				const isConnected = requiredTypes?.some((t) => connectedTypes.has(t));
				if (!isConnected) {
					for (const name of group.tools) placed.add(name);
					continue;
				}
			}

			const matched = group.tools.flatMap((name) => {
				const tool = toolsByName.get(name);
				if (!tool) return [];
				placed.add(name);
				return [tool];
			});
			if (matched.length > 0) {
				result.push({ label: group.label, tools: matched, connectorIcon: group.connectorIcon });
			}
		}

		const ungrouped = filteredTools.filter((t) => !placed.has(t.name));
		if (ungrouped.length > 0) {
			result.push({ label: "Other", tools: ungrouped });
		}

		return result;
	}, [filteredTools, connectedTypes]);
	const regularToolGroups = groupedTools.filter((g) => !g.connectorIcon && g.label !== "Other");
	const connectorToolGroups = groupedTools.filter((g) => g.connectorIcon);
	const otherToolGroup = groupedTools.find((g) => !g.connectorIcon && g.label === "Other");

	useEffect(() => {
		hydrateDisabled();
	}, [hydrateDisabled]);

	// A workspace with no usable chat model renders the composer with an inline
	// notice (see ChatUnavailableNotice) rather than being redirected away, so
	// the send button must stay disabled here. The backend also rejects any
	// send that lacks a resolvable model, making this defense-in-depth.
	const isWorkspaceChatReady = setupStatus?.status === "ready";

	const isSendDisabled = !canSubmitChat({
		isLoadingMessages,
		isThreadRunning,
		isBlockedByOtherUser,
		isComposerEmpty,
		isWorkspaceChatReady,
	});
	const sendTooltip = isLoadingMessages
		? "Loading conversation..."
		: isBlockedByOtherUser
			? "Wait for AI to finish responding"
			: isComposerEmpty
				? "Enter a message or add a screenshot to send"
				: "Send message";

	return (
		<div
			data-desktop-layout-constraint
			className="aui-composer-action-wrapper relative mx-3 mb-3 flex min-w-0 items-center"
		>
			<div data-composer-primary-actions className="flex shrink-0 items-center gap-1">
				{!isDesktop ? (
					<ComposerAddMenuDrawer
						trigger={
							<Button
								variant="ghost"
								size="icon"
								className="h-9 w-9 rounded-full p-0 font-semibold text-xs text-muted-foreground transition-colors dark:border-muted-foreground/15 hover:bg-foreground/10 hover:text-foreground"
								aria-label="Upload files, manage tools and more"
								data-joyride="connector-icon"
							>
								<Plus className="size-5" />
							</Button>
						}
						onUploadFiles={() => openUploadDialog()}
						connectorRows={connectorRows}
						onSelectConnector={openConnectorManage}
						onBrowseConnectors={openConnectors}
						regularToolGroups={regularToolGroups}
						connectorToolGroups={connectorToolGroups}
						otherToolGroup={otherToolGroup}
						disabledToolsSet={disabledToolsSet}
						onToggleTool={toggleTool}
						onToggleToolGroup={toggleToolGroup}
						toolsLoading={!filteredTools?.length}
					/>
				) : (
					<DropdownMenu
						onOpenChange={(open) => {
							if (!open) {
								setToolsPopoverOpen(false);
								setOpenConnectorSubmenu(null);
							}
						}}
					>
						<DropdownMenuTrigger asChild>
							<TooltipIconButton
								tooltip="Upload files, manage tools and more"
								side="bottom"
								disableTooltip={toolsPopoverOpen}
								variant="ghost"
								size="icon"
								className="h-9 w-9 rounded-full p-0 font-semibold text-xs text-muted-foreground transition-colors dark:border-muted-foreground/15 hover:bg-foreground/10 hover:text-foreground"
								aria-label="Upload files, manage tools and more"
								data-joyride="connector-icon"
							>
								<Plus className="size-5" />
							</TooltipIconButton>
						</DropdownMenuTrigger>
						<DropdownMenuContent
							className="w-48"
							side="bottom"
							align="start"
							sideOffset={8}
							onCloseAutoFocus={(event) => event.preventDefault()}
						>
							<DropdownMenuItem onSelect={() => openUploadDialog()}>
								<Upload className="h-4 w-4" />
								Upload Files
							</DropdownMenuItem>
							<DropdownMenuItem onSelect={() => void handleScreenCapture()}>
								<Camera className="h-4 w-4" />
								Take a screenshot
							</DropdownMenuItem>
							{connectorRows.length > 0 ? (
								<DropdownMenuSub>
									<DropdownMenuSubTrigger>
										<Unplug className="h-4 w-4" />
										MCP Connectors
									</DropdownMenuSubTrigger>
									<DropdownMenuPortal>
										<DropdownMenuSubContent
											collisionPadding={8}
											className="w-60 max-h-72 gap-1 overflow-y-auto overscroll-none"
										>
											{connectorRows.map((row) => (
												<DropdownMenuItem
													key={row.type}
													onSelect={() => openConnectorManage(row)}
													className="gap-2"
												>
													{getConnectorIcon(row.type, "h-4 w-4")}
													<span className="min-w-0 flex-1 truncate">{row.title}</span>
													<ConnectorRowStatus row={row} className="text-xs" />
												</DropdownMenuItem>
											))}
											<DropdownMenuSeparator />
											<DropdownMenuItem onSelect={openConnectors} className="gap-2">
												<LayoutGrid className="h-4 w-4" />
												Manage connectors
											</DropdownMenuItem>
										</DropdownMenuSubContent>
									</DropdownMenuPortal>
								</DropdownMenuSub>
							) : (
								<DropdownMenuItem onSelect={openConnectors}>
									<Unplug className="h-4 w-4" />
									MCP Connectors
								</DropdownMenuItem>
							)}
							<DropdownMenuSub
								open={toolsPopoverOpen}
								onOpenChange={(open) => {
									setToolsPopoverOpen(open);
									if (!open) setOpenConnectorSubmenu(null);
								}}
							>
								<DropdownMenuSubTrigger>
									<Settings2 className="h-4 w-4" />
									Manage Tools
								</DropdownMenuSubTrigger>
								<DropdownMenuPortal>
									<DropdownMenuSubContent
										alignOffset={-192}
										collisionPadding={8}
										className="w-60 h-56 gap-1 overflow-y-auto overscroll-none"
										onScroll={() => setOpenConnectorSubmenu(null)}
									>
										{regularToolGroups.map((group) => (
											<div key={group.label}>
												<div className="px-2 pt-1.5 pb-0.5 text-xs text-muted-foreground font-semibold select-none">
													{group.label}
												</div>
												{group.tools.map((tool) => {
													const isDisabled = disabledToolsSet.has(tool.name);
													const ToolIcon = getToolIcon(tool.name);
													return (
														<DropdownMenuItem
															key={tool.name}
															onSelect={(e) => {
																e.preventDefault();
																toggleTool(tool.name);
															}}
															className={cn(
																"mb-1 last:mb-0 transition-all",
																"hover:bg-accent hover:text-accent-foreground",
																!isDisabled && "text-primary"
															)}
														>
															<ToolIcon className="h-4 w-4" />
															<span className="flex-1 min-w-0 truncate">
																{formatToolName(tool.name)}
															</span>
															<Switch
																checked={!isDisabled}
																tabIndex={-1}
																className="pointer-events-none shrink-0 origin-right scale-[0.6]"
															/>
														</DropdownMenuItem>
													);
												})}
											</div>
										))}
										{connectorToolGroups.length > 0 && (
											<div>
												<div className="px-2 pt-1.5 pb-0.5 text-xs text-muted-foreground font-semibold select-none">
													Connector Actions
												</div>
												{connectorToolGroups.map((group) => {
													const iconKey = group.connectorIcon ?? "";
													const iconInfo = CONNECTOR_TOOL_ICON_PATHS[iconKey];
													const toolNames = group.tools.map((t) => t.name);
													const allDisabled = toolNames.every((n) => disabledToolsSet.has(n));
													return (
														<DropdownMenuSub
															key={group.label}
															open={openConnectorSubmenu === group.label}
															onOpenChange={(open) =>
																setOpenConnectorSubmenu(open ? group.label : null)
															}
														>
															<DropdownMenuSubTrigger
																className={cn(
																	"mb-1 last:mb-0 transition-all",
																	"hover:bg-accent hover:text-accent-foreground",
																	"gap-1 [&>svg:last-child]:ml-0",
																	!allDisabled && "text-primary"
																)}
															>
																{iconInfo ? (
																	<Image
																		src={iconInfo.src}
																		alt={iconInfo.alt}
																		width={16}
																		height={16}
																		className="h-4 w-4 shrink-0 select-none pointer-events-none"
																		draggable={false}
																	/>
																) : (
																	<Wrench className="h-4 w-4" />
																)}
																<span className="min-w-0 flex-1 truncate">{group.label}</span>
																<Switch
																	checked={!allDisabled}
																	tabIndex={-1}
																	onPointerDown={(event) => event.stopPropagation()}
																	onClick={(event) => event.stopPropagation()}
																	onCheckedChange={() => toggleToolGroup(toolNames)}
																	className="mr-2 shrink-0 origin-right scale-[0.6]"
																/>
															</DropdownMenuSubTrigger>
															<DropdownMenuPortal>
																<DropdownMenuSubContent
																	collisionPadding={8}
																	className="w-60 max-h-56 overflow-y-auto overscroll-none"
																>
																	{group.tools.map((tool) => {
																		const isDisabled = disabledToolsSet.has(tool.name);
																		return (
																			<DropdownMenuItem
																				key={tool.name}
																				onSelect={(e) => {
																					e.preventDefault();
																					toggleTool(tool.name);
																				}}
																				className={cn(
																					"mb-1 last:mb-0 transition-all",
																					"hover:bg-accent hover:text-accent-foreground",
																					!isDisabled && "text-primary"
																				)}
																			>
																				<span className="min-w-0 flex-1 truncate">
																					{formatToolName(tool.name)}
																				</span>
																				<Switch
																					checked={!isDisabled}
																					tabIndex={-1}
																					className="pointer-events-none shrink-0 origin-right scale-[0.6]"
																				/>
																			</DropdownMenuItem>
																		);
																	})}
																</DropdownMenuSubContent>
															</DropdownMenuPortal>
														</DropdownMenuSub>
													);
												})}
											</div>
										)}
										{otherToolGroup && (
											<div>
												<div className="px-2 pt-1.5 pb-0.5 text-xs text-muted-foreground font-semibold select-none">
													{otherToolGroup.label}
												</div>
												{otherToolGroup.tools.map((tool) => {
													const isDisabled = disabledToolsSet.has(tool.name);
													const ToolIcon = getToolIcon(tool.name);
													return (
														<DropdownMenuItem
															key={tool.name}
															onSelect={(e) => {
																e.preventDefault();
																toggleTool(tool.name);
															}}
															className={cn(
																"mb-1 last:mb-0 transition-all",
																"hover:bg-accent hover:text-accent-foreground",
																!isDisabled && "text-primary"
															)}
														>
															<ToolIcon className="h-4 w-4" />
															<span className="flex-1 min-w-0 truncate">
																{formatToolName(tool.name)}
															</span>
															<Switch
																checked={!isDisabled}
																tabIndex={-1}
																className="pointer-events-none shrink-0 origin-right scale-[0.6]"
															/>
														</DropdownMenuItem>
													);
												})}
											</div>
										)}
										{!filteredTools?.length && (
											<div className="px-2 pt-1.5 pb-1">
												<Skeleton className="h-2 w-12 mb-1.5" />
												{["dt1", "dt2", "dt3", "dt4"].map((k) => (
													<div key={k} className="flex items-center gap-2 py-1">
														<Skeleton className="h-4 w-4 rounded shrink-0" />
														<Skeleton className="h-3 flex-1" />
														<Skeleton className="h-4 w-8 rounded-full shrink-0" />
													</div>
												))}
											</div>
										)}
									</DropdownMenuSubContent>
								</DropdownMenuPortal>
							</DropdownMenuSub>
						</DropdownMenuContent>
					</DropdownMenu>
				)}
				<ComposerToolsMenu
					selected={preferredTool}
					onSelect={setPreferredTool}
					connected={connectedKeyedTools}
					compact={!isDesktop}
				/>
				<NotebookSourcesChip />
				<InstructionsAppliedBadge workspaceId={workspaceId} compact={!isDesktop} />
			</div>
			<div
				data-composer-model-actions
				className="ml-auto flex min-w-0 flex-1 items-center justify-end gap-2"
			>
				<ChatHeader
					workspaceId={workspaceId}
					className="h-9 px-2"
					onChatModelSelected={onChatModelSelected}
				/>
				<AuiIf condition={({ thread }) => !thread.isRunning}>
					<TooltipIconButton
						tooltip={sendTooltip}
						side="bottom"
						type="button"
						variant="default"
						size="icon"
						className={cn(
							"aui-composer-send size-9 shrink-0 rounded-full",
							isSendDisabled && "cursor-not-allowed opacity-50"
						)}
						aria-label="Send message"
						disabled={isSendDisabled}
						onClick={onSend}
					>
						<ArrowUpIcon className="aui-composer-send-icon size-5" />
					</TooltipIconButton>
				</AuiIf>

				<AuiIf condition={({ thread }) => thread.isRunning}>
					{turnStopping ? (
						// Stop was pressed (here or on another page) and the backend is
						// still saving the partial reply: a second Stop would do nothing.
						<Button
							type="button"
							variant="secondary"
							size="sm"
							disabled
							aria-live="polite"
							className="aui-composer-stopping h-9 shrink-0 gap-2 rounded-full px-3 disabled:opacity-100"
						>
							<Spinner size="xs" hideTrack />
							Stopping…
						</Button>
					) : (
						<ComposerPrimitive.Cancel asChild>
							<Button
								type="button"
								variant="default"
								size="icon"
								className="aui-composer-cancel size-9 shrink-0 rounded-full"
								aria-label="Stop generating"
							>
								<SquareIcon className="aui-composer-cancel-icon size-3.5 fill-current" />
							</Button>
						</ComposerPrimitive.Cancel>
					)}
				</AuiIf>
			</div>
		</div>
	);
};

/** Friendly tool name (delegates to ``getToolDisplayName``). */
function formatToolName(name: string): string {
	return getToolDisplayName(name);
}

interface ToolGroup {
	label: string;
	tools: string[];
	connectorIcon?: string;
	tooltip?: string;
}

const TOOL_GROUPS: ToolGroup[] = [
	{
		label: "Research",
		tools: ["scrape_webpage"],
	},
	{
		label: "Generate",
		tools: ["generate_podcast", "generate_video_presentation", "generate_image"],
	},
	{
		label: "Memory",
		tools: ["update_memory"],
	},
	{
		label: "Google Drive",
		tools: ["create_google_drive_file", "delete_google_drive_file"],
		connectorIcon: "google_drive",
		tooltip: "Create and delete files in Google Drive",
	},
];

const EditComposer: FC = () => {
	return (
		<MessagePrimitive.Root className="aui-edit-composer-wrapper mx-auto flex w-full max-w-(--thread-max-width) flex-col px-2 py-3">
			<ComposerPrimitive.Root className="aui-edit-composer-root ml-auto flex w-full max-w-[85%] flex-col rounded-2xl bg-muted">
				<ComposerPrimitive.Input
					className="aui-edit-composer-input min-h-14 w-full resize-none bg-transparent p-4 text-foreground text-sm outline-none"
					autoFocus
				/>
				<div className="aui-edit-composer-footer mx-3 mb-3 flex items-center gap-2 self-end">
					<ComposerPrimitive.Cancel asChild>
						<Button variant="ghost" size="sm">
							Cancel
						</Button>
					</ComposerPrimitive.Cancel>
					<ComposerPrimitive.Send asChild>
						<Button size="sm">Update</Button>
					</ComposerPrimitive.Send>
				</div>
			</ComposerPrimitive.Root>
		</MessagePrimitive.Root>
	);
};
