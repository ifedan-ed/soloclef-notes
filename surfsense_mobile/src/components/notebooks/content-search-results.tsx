import { ActivityIndicator, StyleSheet, View } from "react-native";
import type { NotebookSearchHit, SearchPassage } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { relativeTime, snippetParts } from "../../lib/util/notebooks";
import { radius, space } from "../../theme/colors";
import { AppText, Icon, PressableRow, useDelayedFlag } from "../ui/primitives";

/**
 * "Found inside notebooks" under the title-filtered list (web:
 * ui/home/content-search-results.tsx). Earlier results stay painted while a
 * refined query loads; the spinner only shows after 250 ms.
 */
export function ContentSearchResults({
	query,
	hits,
	pending,
	settled,
	failed,
	onOpenNotebook,
	onOpenPassage,
}: {
	query: string;
	hits: NotebookSearchHit[];
	pending: boolean;
	settled: boolean;
	failed: boolean;
	onOpenNotebook: (notebookId: number) => void;
	onOpenPassage: (notebookId: number, passage: SearchPassage) => void;
}) {
	const { t, locale, colors } = useApp();
	const spinner = useDelayedFlag(pending);
	return (
		<View style={styles.section}>
			<View style={styles.heading}>
				<AppText variant="label" tone="muted" accessibilityRole="header">
					{t("notebooks.search_inside_title")}
				</AppText>
				<View style={styles.spinnerSlot}>
					{spinner ? <ActivityIndicator size="small" color={colors.mutedForeground} /> : null}
				</View>
			</View>
			{failed ? (
				<AppText tone="muted" variant="small">
					{t("notebooks.search_inside_failed")}
				</AppText>
			) : settled && hits.length === 0 ? (
				<AppText tone="muted" variant="small">
					{t("notebooks.search_inside_empty", { query })}
				</AppText>
			) : null}
			{hits.map((hit) => (
				<View key={hit.id} style={[styles.card, { borderColor: colors.border }]}>
					<PressableRow
						onPress={() => onOpenNotebook(hit.id)}
						accessibilityRole="button"
						accessibilityLabel={hit.title}
						style={styles.notebookRow}
					>
						<AppText style={{ fontSize: 18 }}>{hit.icon || "📓"}</AppText>
						<AppText variant="subheading" numberOfLines={1} style={{ flex: 1, minWidth: 0 }}>
							{hit.title}
						</AppText>
						<AppText variant="caption" tone="muted">
							{t("notebooks.search_inside_updated", { when: relativeTime(hit.updated_at, locale) })}
						</AppText>
					</PressableRow>
					{hit.passages.map((passage) => (
						<PressableRow
							key={`${passage.kind}-${passage.note_id ?? passage.document_id}-${passage.source_id}-${passage.score}-${passage.snippet.length}`}
							onPress={() => onOpenPassage(hit.id, passage)}
							accessibilityRole="button"
							accessibilityLabel={`${
								passage.kind === "note" ? t("notebooks.search_inside_note") : t("notebooks.search_inside_source")
							}: ${passage.title}. ${passage.snippet}`}
							style={styles.passage}
						>
							<View style={{ flex: 1, minWidth: 0, gap: 2 }}>
								<View style={styles.passageName}>
									<Icon
										name={
											passage.kind === "note"
												? "create-outline"
												: passage.folder_name
													? "folder-outline"
													: "document-text-outline"
										}
										size={14}
										color={colors.mutedForeground}
									/>
									<AppText variant="caption" tone="muted" numberOfLines={1} style={{ flex: 1 }}>
										{passage.folder_name ? `${passage.folder_name}/${passage.title}` : passage.title}
									</AppText>
								</View>
								<AppText variant="small" numberOfLines={3}>
									{snippetParts(passage.snippet, passage.highlights).map((part) => (
										<AppText
											key={part.start}
											variant="small"
											style={part.hit ? { backgroundColor: colors.citation, fontWeight: "600" } : undefined}
										>
											{part.text}
										</AppText>
									))}
								</AppText>
							</View>
						</PressableRow>
					))}
				</View>
			))}
		</View>
	);
}

const styles = StyleSheet.create({
	section: { paddingHorizontal: space.lg, marginTop: space.lg, gap: space.sm },
	heading: { flexDirection: "row", alignItems: "center", gap: space.sm },
	spinnerSlot: { width: 20, height: 20, alignItems: "center", justifyContent: "center" },
	card: { borderWidth: 1, borderRadius: radius.lg, overflow: "hidden" },
	notebookRow: { gap: space.sm, alignItems: "center" },
	passage: { paddingTop: 6, paddingBottom: 8 },
	passageName: { flexDirection: "row", alignItems: "center", gap: 4 },
});
