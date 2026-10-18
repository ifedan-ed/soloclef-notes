import { useEffect, useMemo, useState } from "react";
import { Keyboard, Pressable, ScrollView, StyleSheet, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { NotebookSource } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import {
	activeMentionQuery,
	type MentionedSource,
	mentionFromSource,
	stripActiveMention,
} from "../../lib/chat/request";
import { radius, space } from "../../theme/colors";
import { sourceIcon } from "../sources/sources-pane";
import { AppText, Chip, Icon } from "../ui/primitives";

/**
 * The message box. Typing "@" lists the notebook's sources (only this
 * notebook's, as on the web); picking one adds a chip that narrows the next
 * answer to it.
 */
export function Composer({
	sources,
	running,
	disabled,
	onSend,
	onStop,
	restore,
}: {
	sources: NotebookSource[];
	running: boolean;
	disabled: boolean;
	onSend: (text: string, mentions: MentionedSource[]) => void;
	onStop: () => void;
	/** A send that failed before the server took it: put its text and chips back. */
	restore: { draft: string; mentions: MentionedSource[] } | null;
}) {
	const { t, colors, scheme } = useApp();
	const insets = useSafeAreaInsets();
	const [text, setText] = useState("");
	const [mentions, setMentions] = useState<MentionedSource[]>([]);
	const [keyboardOpen, setKeyboardOpen] = useState(false);

	// A failed send gives its text back: adjust state while rendering (not in an
	// effect) when a new restore arrives.
	const [restored, setRestored] = useState(restore);
	if (restore !== restored) {
		setRestored(restore);
		if (restore) {
			setText(restore.draft);
			setMentions(restore.mentions);
		}
	}

	useEffect(() => {
		const show = Keyboard.addListener("keyboardDidShow", () => setKeyboardOpen(true));
		const hide = Keyboard.addListener("keyboardDidHide", () => setKeyboardOpen(false));
		return () => {
			show.remove();
			hide.remove();
		};
	}, []);

	const query = activeMentionQuery(text);
	const matches = useMemo(() => {
		if (query === null) return [];
		const needle = query.trim().toLowerCase();
		const picked = new Set(mentions.map((m) => `${m.kind}:${m.id}`));
		return sources
			.filter((s) => !needle || s.title.toLowerCase().includes(needle))
			.filter((s) => !picked.has(`${s.kind === "folder" ? "folder" : "doc"}:${s.target_id}`))
			.slice(0, 8);
	}, [query, sources, mentions]);

	const submit = () => {
		const value = text.trim();
		if (!value || running || disabled) return;
		onSend(value, mentions);
		setText("");
		setMentions([]);
	};

	const pick = (source: NotebookSource) => {
		setMentions((current) => [...current, mentionFromSource(source)]);
		setText((current) => stripActiveMention(current));
	};

	return (
		<View
			style={[
				styles.wrap,
				{
					borderTopColor: colors.border,
					backgroundColor: colors.background,
					paddingBottom: keyboardOpen ? space.sm : insets.bottom + space.sm,
				},
			]}
		>
			{query !== null ? (
				<View style={[styles.popover, { borderColor: colors.border, backgroundColor: colors.popover }]}>
					<AppText variant="caption" tone="muted" style={{ paddingHorizontal: space.md, paddingTop: space.sm }}>
						{t("notebooks.mention_heading")}
					</AppText>
					{sources.length === 0 ? (
						<AppText variant="small" tone="muted" style={styles.popoverEmpty}>
							{t("notebooks.mention_no_sources")}
						</AppText>
					) : matches.length === 0 ? (
						<AppText variant="small" tone="muted" style={styles.popoverEmpty}>
							{t("notebooks.mention_no_match")}
						</AppText>
					) : (
						<ScrollView keyboardShouldPersistTaps="always" style={{ maxHeight: 220 }}>
							{matches.map((source) => (
								<Pressable
									key={source.id}
									onPress={() => pick(source)}
									accessibilityRole="button"
									style={({ pressed }) => [styles.match, { backgroundColor: pressed ? colors.accent : "transparent" }]}
								>
									<Icon name={sourceIcon(source)} size={18} color={colors.mutedForeground} />
									<AppText numberOfLines={1} style={{ flex: 1 }}>
										{source.title}
									</AppText>
								</Pressable>
							))}
						</ScrollView>
					)}
				</View>
			) : null}
			{mentions.length > 0 ? (
				<ScrollView
					horizontal
					showsHorizontalScrollIndicator={false}
					contentContainerStyle={styles.chips}
					keyboardShouldPersistTaps="always"
				>
					{mentions.map((m) => (
						<Chip
							key={`${m.kind}-${m.id}`}
							icon={m.kind === "folder" ? "folder-outline" : "document-text-outline"}
							label={m.title}
							onRemove={() => setMentions((current) => current.filter((c) => !(c.kind === m.kind && c.id === m.id)))}
							removeLabel={t("notebooks.remove_pick", { title: m.title })}
						/>
					))}
				</ScrollView>
			) : null}
			<View style={[styles.box, { borderColor: colors.input, backgroundColor: colors.background }]}>
				<TextInput
					value={text}
					onChangeText={setText}
					placeholder={t("mobile.chat_placeholder")}
					placeholderTextColor={colors.mutedForeground}
					keyboardAppearance={scheme}
					multiline
					editable={!disabled}
					accessibilityLabel={t("mobile.chat_placeholder")}
					accessibilityHint={t("mobile.mention_hint")}
					style={[styles.input, { color: colors.foreground }]}
				/>
				{running ? (
					<Pressable
						onPress={onStop}
						accessibilityRole="button"
						accessibilityLabel={t("mobile.stop")}
						style={[styles.send, { backgroundColor: colors.primary }]}
					>
						<Icon name="stop" size={16} color={colors.primaryForeground} />
					</Pressable>
				) : (
					<Pressable
						onPress={submit}
						disabled={!text.trim() || disabled}
						accessibilityRole="button"
						accessibilityLabel={t("mobile.send")}
						style={[styles.send, { backgroundColor: colors.primary, opacity: !text.trim() || disabled ? 0.4 : 1 }]}
					>
						<Icon name="arrow-up" size={18} color={colors.primaryForeground} />
					</Pressable>
				)}
			</View>
		</View>
	);
}

const styles = StyleSheet.create({
	wrap: { borderTopWidth: StyleSheet.hairlineWidth, paddingHorizontal: space.md, paddingTop: space.sm, gap: space.sm },
	box: {
		flexDirection: "row",
		alignItems: "flex-end",
		borderWidth: 1,
		borderRadius: radius.xl,
		paddingLeft: space.md,
		paddingRight: 6,
		paddingVertical: 6,
	},
	input: { flex: 1, fontSize: 16, lineHeight: 22, maxHeight: 140, minHeight: 32, paddingTop: 6, paddingBottom: 6 },
	send: {
		width: 34,
		height: 34,
		borderRadius: 17,
		alignItems: "center",
		justifyContent: "center",
		marginLeft: space.sm,
	},
	chips: { gap: space.sm },
	popover: { borderWidth: 1, borderRadius: radius.md, paddingBottom: space.xs },
	popoverEmpty: { padding: space.md },
	match: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		paddingHorizontal: space.md,
		paddingVertical: 10,
	},
});
