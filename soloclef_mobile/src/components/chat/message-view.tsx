import * as Clipboard from "expo-clipboard";
import { memo, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, View } from "react-native";
import { useApp } from "../../lib/app-context";
import { billingAction } from "../../lib/billing";
import { answerText, type ContentPart } from "../../lib/chat/stream-state";
import type { ChatMessage } from "../../lib/chat/use-notebook-chat";
import { radius, space } from "../../theme/colors";
import { useBillingNoticeAction } from "../billing-link";
import { type CitationHandler, MarkdownView } from "../markdown-view";
import { AppText, Button, Icon, Notice } from "../ui/primitives";

/** "search_knowledge_base" → "search knowledge base". */
function humanizeTool(name: string): string {
	return name.replace(/[_-]+/g, " ").replace(/\s+/g, " ").trim();
}

export const MessageView = memo(function MessageView({
	message,
	onCitation,
	onRetry,
}: {
	message: ChatMessage;
	onCitation: CitationHandler;
	onRetry?: () => void;
}) {
	const { colors } = useApp();
	if (message.role === "user") {
		return (
			<View style={styles.userWrap}>
				{message.mentions && message.mentions.length > 0 ? (
					<View style={styles.mentionRow}>
						{message.mentions.map((title) => (
							<View key={title} style={[styles.mention, { borderColor: colors.border }]}>
								<Icon name="at" size={12} color={colors.mutedForeground} />
								<AppText variant="caption" numberOfLines={1}>
									{title}
								</AppText>
							</View>
						))}
					</View>
				) : null}
				<View style={[styles.userBubble, { backgroundColor: colors.secondary }]}>
					<AppText selectable>{answerText(message.parts)}</AppText>
				</View>
			</View>
		);
	}
	return <AssistantMessage message={message} onCitation={onCitation} onRetry={onRetry} />;
});

function AssistantMessage({
	message,
	onCitation,
	onRetry,
}: {
	message: ChatMessage;
	onCitation: CitationHandler;
	onRetry?: () => void;
}) {
	const { t, colors } = useApp();
	const billingNotice = useBillingNoticeAction();
	const [copied, setCopied] = useState(false);
	const streaming = message.status === "streaming";
	const hasText = message.parts.some((p) => p.type === "text" && p.text.trim());
	const text = answerText(message.parts);

	return (
		<View style={styles.assistant}>
			{message.parts.map((part, index) => (
				<PartView key={index} part={part} onCitation={onCitation} />
			))}
			{streaming && !hasText ? (
				<View style={styles.working}>
					<ActivityIndicator size="small" color={colors.mutedForeground} />
					<AppText variant="small" tone="muted">
						{t("mobile.working")}
					</AppText>
				</View>
			) : null}
			{message.truncated ? <Notice message={t("mobile.answer_cut_off")} /> : null}
			{message.status === "error" ? (
				<Notice
					tone="error"
					message={message.error ?? t("mobile.answer_failed")}
					actionLabel={onRetry ? t("notebooks.try_again") : undefined}
					onAction={onRetry}
					{...billingNotice(billingAction(message.errorCode))}
				/>
			) : null}
			{!streaming && hasText ? (
				<View style={styles.actions}>
					<Button
						small
						kind="ghost"
						icon={copied ? "checkmark" : "copy-outline"}
						title={copied ? t("mobile.copied") : t("mobile.copy")}
						onPress={async () => {
							await Clipboard.setStringAsync(text);
							setCopied(true);
							setTimeout(() => setCopied(false), 1500);
						}}
					/>
				</View>
			) : null}
		</View>
	);
}

function PartView({ part, onCitation }: { part: ContentPart; onCitation: CitationHandler }) {
	const { t, colors } = useApp();
	const [open, setOpen] = useState(false);
	if (part.type === "text") return <MarkdownView text={part.text} onCitation={onCitation} />;
	if (part.type === "reasoning") {
		return (
			<Pressable
				onPress={() => setOpen((v) => !v)}
				accessibilityRole="button"
				accessibilityState={{ expanded: open }}
				style={[styles.reasoning, { borderColor: colors.border }]}
			>
				<View style={styles.reasoningHead}>
					{part.status === "running" ? (
						<ActivityIndicator size="small" color={colors.mutedForeground} />
					) : (
						<Icon name="bulb-outline" size={16} color={colors.mutedForeground} />
					)}
					<AppText variant="small" tone="muted" style={{ flex: 1 }}>
						{t("mobile.thinking")}
					</AppText>
					<Icon name={open ? "chevron-up" : "chevron-down"} size={16} color={colors.mutedForeground} />
				</View>
				{open ? (
					<AppText variant="small" tone="muted" selectable>
						{part.text}
					</AppText>
				) : null}
			</Pressable>
		);
	}
	const done = part.result !== undefined;
	return (
		<View style={styles.tool}>
			{done ? (
				<Icon name="checkmark-circle-outline" size={16} color={colors.mutedForeground} />
			) : (
				<ActivityIndicator size="small" color={colors.mutedForeground} />
			)}
			<AppText variant="small" tone="muted" numberOfLines={1} style={{ flex: 1 }}>
				{t("mobile.used_tool", { tool: humanizeTool(part.toolName) })}
			</AppText>
		</View>
	);
}

const styles = StyleSheet.create({
	userWrap: { alignItems: "flex-end", gap: 6, paddingLeft: space.xxl },
	userBubble: {
		borderRadius: radius.lg,
		borderBottomRightRadius: radius.sm,
		paddingHorizontal: space.md,
		paddingVertical: 10,
		maxWidth: "100%",
	},
	mentionRow: { flexDirection: "row", flexWrap: "wrap", gap: 6, justifyContent: "flex-end" },
	mention: {
		flexDirection: "row",
		alignItems: "center",
		gap: 4,
		borderWidth: 1,
		borderRadius: radius.pill,
		paddingHorizontal: 8,
		paddingVertical: 3,
		maxWidth: 220,
	},
	assistant: { gap: space.sm },
	working: { flexDirection: "row", alignItems: "center", gap: space.sm },
	actions: { flexDirection: "row", marginLeft: -space.md },
	reasoning: { borderWidth: 1, borderRadius: radius.md, padding: space.sm, gap: space.sm },
	reasoningHead: { flexDirection: "row", alignItems: "center", gap: space.sm },
	tool: { flexDirection: "row", alignItems: "center", gap: space.sm },
});
