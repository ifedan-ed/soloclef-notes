import { useQuery } from "@tanstack/react-query";
import * as WebBrowser from "expo-web-browser";
import { useCallback, useMemo, useState } from "react";
import { FlatList, KeyboardAvoidingView, StyleSheet, View } from "react-native";
import type { Notebook } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import type { CitationToken } from "../../lib/chat/citations";
import { billingAction } from "../../lib/billing";
import { answerText } from "../../lib/chat/stream-state";
import { type ChatMessage, useNotebookChat } from "../../lib/chat/use-notebook-chat";
import { keys, useSources, useSuggestions } from "../../lib/queries";
import { space } from "../../theme/colors";
import { useBillingNoticeAction } from "../billing-link";
import { AppText, Button, Chip, DelayedSpinner, Notice, useDelayedFlag } from "../ui/primitives";
import { CitationSheet } from "./citation-sheet";
import { Composer } from "./composer";
import { MessageView } from "./message-view";

const GUIDED_STARTS = ["learn", "create", "progress"] as const;

export function ChatPane({
	workspaceId,
	notebookId,
	notebook,
	onAddSources,
}: {
	workspaceId: number;
	notebookId: number;
	notebook: Notebook | undefined;
	onAddSources: () => void;
}) {
	const { t } = useApp();
	const chat = useNotebookChat(workspaceId, notebookId);
	const billingNotice = useBillingNoticeAction();
	const sources = useSources(workspaceId, notebookId);
	const [citation, setCitation] = useState<{ token: CitationToken; number: number } | null>(null);
	const showLoader = useDelayedFlag(chat.loading && chat.messages.length === 0);

	const onCitation = useCallback((token: CitationToken, number: number) => {
		if (token.kind === "url") {
			void WebBrowser.openBrowserAsync(token.url);
			return;
		}
		if (token.kind === "chunk") setCitation({ token, number });
	}, []);

	// Newest first for the inverted list: the latest message sits above the composer.
	const reversed = useMemo(() => [...chat.messages].reverse(), [chat.messages]);
	const lastAssistant = chat.messages[chat.messages.length - 1];
	const lastUserText = [...chat.messages].reverse().find((m) => m.role === "user");

	const retry = () => {
		if (lastUserText) void chat.send(answerText(lastUserText.parts));
	};

	return (
		<KeyboardAvoidingView behavior="padding" style={{ flex: 1 }}>
			{chat.failed && chat.messages.length === 0 ? (
				<View style={{ padding: space.lg }}>
					<Notice
						tone="error"
						message={t("notebooks.new_chat_failed")}
						actionLabel={t("notebooks.retry")}
						onAction={chat.retryLoad}
					/>
				</View>
			) : null}
			{chat.messages.length === 0 && !chat.loading ? (
				<Welcome
					workspaceId={workspaceId}
					notebookId={notebookId}
					notebook={notebook}
					onAsk={(text) => void chat.send(text)}
					onAddSources={onAddSources}
				/>
			) : chat.messages.length === 0 ? (
				<View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
					{showLoader ? <DelayedSpinner active /> : null}
				</View>
			) : (
				<FlatList
					inverted
					data={reversed}
					keyExtractor={(m) => m.key}
					keyboardShouldPersistTaps="handled"
					keyboardDismissMode="interactive"
					contentContainerStyle={styles.list}
					ItemSeparatorComponent={() => <View style={{ height: space.xl }} />}
					ListHeaderComponent={
						lastAssistant && lastAssistant.role === "assistant" && lastAssistant.status === "done" && !chat.running ? (
							<Followups
								workspaceId={workspaceId}
								notebookId={notebookId}
								message={lastAssistant}
								onAsk={(text) => void chat.send(text)}
							/>
						) : null
					}
					renderItem={({ item, index }) => (
						<MessageView
							message={item}
							onCitation={onCitation}
							onRetry={index === 0 && item.status === "error" ? retry : undefined}
						/>
					)}
				/>
			)}
			{chat.sendError ? (
				<View style={{ paddingHorizontal: space.lg, paddingBottom: space.sm }}>
					<Notice
						tone="error"
						message={chat.sendError.message}
						onDismiss={chat.clearSendError}
						{...billingNotice(billingAction(chat.sendError.code, chat.sendError.status))}
					/>
				</View>
			) : null}
			<Composer
				sources={sources.data ?? []}
				running={chat.running}
				disabled={chat.threadId <= 0}
				onSend={(text, mentions) => void chat.send(text, mentions)}
				onStop={() => void chat.stop()}
				restore={chat.sendError}
			/>
			<CitationSheet citation={citation} onClose={() => setCitation(null)} />
		</KeyboardAvoidingView>
	);
}

/** The empty chat: the notebook, then three questions from its sources (or guided starts). */
function Welcome({
	workspaceId,
	notebookId,
	notebook,
	onAsk,
	onAddSources,
}: {
	workspaceId: number;
	notebookId: number;
	notebook: Notebook | undefined;
	onAsk: (text: string) => void;
	onAddSources: () => void;
}) {
	const { t } = useApp();
	const hasSources = (notebook?.enabled_source_count ?? 0) > 0;
	const questions = useSuggestions(workspaceId, notebookId, "chat", {}, hasSources);
	const showSkeleton = useDelayedFlag(hasSources && questions.isLoading, 400);
	const suggested = questions.data?.suggestions ?? [];
	const guided = !hasSources || (!questions.isLoading && suggested.length === 0);

	return (
		<FlatList
			data={[]}
			renderItem={null}
			keyboardShouldPersistTaps="handled"
			contentContainerStyle={styles.welcome}
			ListHeaderComponent={
				<View style={{ gap: space.lg }}>
					<View style={{ gap: space.sm }}>
						<AppText style={{ fontSize: 44, lineHeight: 52 }}>{notebook?.icon || "📓"}</AppText>
						<AppText variant="title">
							{hasSources ? t("notebooks.welcome.heading_with_sources") : t("notebooks.welcome.heading")}
						</AppText>
						<AppText tone="muted">
							{hasSources ? t("notebooks.welcome.body_with_sources") : t("notebooks.welcome.body_no_sources")}
						</AppText>
						{notebook ? (
							<AppText variant="small" tone="muted">
								{t("notebooks.source_count", { count: notebook.source_count })}
							</AppText>
						) : null}
					</View>
					{guided ? (
						<View style={{ gap: space.sm }}>
							{GUIDED_STARTS.map((kind) => (
								<Chip
									key={kind}
									icon="sparkles-outline"
									label={t(`notebooks.welcome.starts.${kind}.label`)}
									onPress={() => onAsk(t(`notebooks.welcome.starts.${kind}.prompt`))}
								/>
							))}
						</View>
					) : suggested.length > 0 ? (
						<View style={{ gap: space.sm }} accessibilityLabel={t("notebooks.welcome.questions_label")}>
							{suggested.slice(0, 3).map((question) => (
								<Chip
									key={question}
									icon="chatbubble-ellipses-outline"
									label={question}
									onPress={() => onAsk(question)}
								/>
							))}
						</View>
					) : showSkeleton ? (
						<DelayedSpinner active />
					) : null}
					{!hasSources ? (
						<Button kind="outline" icon="add" title={t("notebooks.add_sources")} onPress={onAddSources} />
					) : null}
				</View>
			}
		/>
	);
}

/** Three suggested next questions under the latest answer (web: NotebookFollowups). */
function Followups({
	workspaceId,
	notebookId,
	message,
	onAsk,
}: {
	workspaceId: number;
	notebookId: number;
	message: ChatMessage;
	onAsk: (text: string) => void;
}) {
	const { ep, t } = useApp();
	const answer = answerText(message.parts);
	const followups = useQuery({
		queryKey: keys.followups(workspaceId, notebookId, String(message.dbId ?? message.key)),
		queryFn: () => ep.followups(workspaceId, notebookId, answer),
		enabled: answer.trim().length > 0,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	const suggestions = followups.data?.suggestions ?? [];
	if (suggestions.length === 0) return null;
	return (
		<View style={{ gap: space.sm, paddingTop: space.lg }} accessibilityLabel={t("notebooks.followups_label")}>
			{suggestions.slice(0, 3).map((question) => (
				<Chip key={question} icon="return-down-forward-outline" label={question} onPress={() => onAsk(question)} />
			))}
		</View>
	);
}

const styles = StyleSheet.create({
	list: { paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.lg },
	welcome: { padding: space.lg, paddingTop: space.xl },
});
