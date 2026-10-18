import { useMemo, useState } from "react";
import { Pressable, ScrollView, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { ArtifactManifest } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { flashcardDeckSchema, shuffled, useArtifactJson } from "../../lib/artifacts";
import { useWorkspace } from "../../lib/queries";
import { radius, space } from "../../theme/colors";
import { MarkdownView } from "../markdown-view";
import { AppText, Button, DelayedSpinner, Notice } from "../ui/primitives";

/** Native flashcards: tap to flip, hint on demand, previous / next, shuffle. */
export function FlashcardsViewer({ manifest }: { manifest: ArtifactManifest }) {
	const { t, colors } = useApp();
	const insets = useSafeAreaInsets();
	const { workspaceId } = useWorkspace();
	const deck = useArtifactJson(workspaceId, manifest, flashcardDeckSchema);
	const [order, setOrder] = useState<number[] | null>(null);
	const [index, setIndex] = useState(0);
	const [flipped, setFlipped] = useState(false);
	const [hint, setHint] = useState(false);

	const cards = useMemo(() => deck.data?.cards ?? [], [deck.data]);
	const sequence = useMemo(() => order ?? cards.map((_, i) => i), [order, cards]);

	if (deck.isLoading) return <DelayedSpinner active style={{ marginTop: space.xxl }} />;
	if (deck.isError || cards.length === 0) {
		return (
			<View style={{ padding: space.lg }}>
				<Notice
					tone="error"
					message={t("mobile.output_failed")}
					actionLabel={t("notebooks.retry")}
					onAction={() => deck.refetch()}
				/>
			</View>
		);
	}

	const card = cards[sequence[index]];
	const go = (next: number) => {
		setIndex(Math.max(0, Math.min(cards.length - 1, next)));
		setFlipped(false);
		setHint(false);
	};

	return (
		<ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: insets.bottom + space.xxl }}>
			<AppText variant="small" tone="muted" style={{ textAlign: "center" }}>
				{t("mobile.card_position", { current: index + 1, total: cards.length })}
			</AppText>
			<Pressable
				onPress={() => setFlipped((f) => !f)}
				accessibilityRole="button"
				accessibilityHint={flipped ? t("mobile.show_question") : t("mobile.show_answer")}
				style={[styles.card, { borderColor: colors.border, backgroundColor: flipped ? colors.muted : colors.card }]}
			>
				<AppText variant="caption" tone="muted" style={{ textAlign: "center" }}>
					{flipped ? t("mobile.show_question") : t("mobile.show_answer")}
				</AppText>
				<MarkdownView text={flipped ? card.back_text : card.front_text} />
			</Pressable>
			{card.hint_text && !flipped ? (
				hint ? (
					<Notice message={card.hint_text} />
				) : (
					<Button kind="ghost" small icon="bulb-outline" title={t("mobile.show_hint")} onPress={() => setHint(true)} />
				)
			) : null}
			<View style={styles.nav}>
				<Button
					kind="outline"
					icon="chevron-back"
					title={t("mobile.previous")}
					onPress={() => go(index - 1)}
					disabled={index === 0}
					style={{ flex: 1 }}
				/>
				<Button
					kind="primary"
					title={t("mobile.next")}
					onPress={() => go(index + 1)}
					disabled={index === cards.length - 1}
					style={{ flex: 1 }}
				/>
			</View>
			<View style={styles.nav}>
				<Button
					kind="ghost"
					icon="shuffle"
					title={t("mobile.shuffle")}
					onPress={() => {
						setOrder(shuffled(cards.map((_, i) => i)));
						go(0);
					}}
					style={{ flex: 1 }}
				/>
				<Button
					kind="ghost"
					icon="refresh"
					title={t("mobile.restart")}
					onPress={() => {
						setOrder(null);
						go(0);
					}}
					style={{ flex: 1 }}
				/>
			</View>
		</ScrollView>
	);
}

const styles = StyleSheet.create({
	card: {
		minHeight: 260,
		borderWidth: 1,
		borderRadius: radius.xl,
		padding: space.xl,
		gap: space.md,
		justifyContent: "center",
	},
	nav: { flexDirection: "row", gap: space.sm },
});
