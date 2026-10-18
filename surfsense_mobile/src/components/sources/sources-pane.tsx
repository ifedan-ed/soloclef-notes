import { useRouter } from "expo-router";
import { useState } from "react";
import { ActivityIndicator, FlatList, RefreshControl, StyleSheet, Switch, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { NotebookSource } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { useSourceMutations, useSources } from "../../lib/queries";
import { isIndexing } from "../../lib/util/notebooks";
import { space } from "../../theme/colors";
import { useBillingNoticeAction } from "../billing-link";
import { Sheet, useToast } from "../ui/layout";
import { AppText, Button, Checkbox, DelayedSpinner, Icon, type IconName, Notice, PressableRow } from "../ui/primitives";
import { AddSourcesSheet, type UploadState } from "./add-sources-sheet";

export function sourceIcon(source: Pick<NotebookSource, "kind" | "document_type">): IconName {
	if (source.kind === "folder") return "folder-outline";
	switch (source.document_type) {
		case "YOUTUBE_VIDEO":
			return "logo-youtube";
		case "CRAWLED_URL":
		case "LINKUP_API":
		case "TAVILY_API":
			return "globe-outline";
		case "NOTE":
			return "create-outline";
		case "FILE":
			return "document-text-outline";
		default:
			return "document-outline";
	}
}

export function SourcesPane({ workspaceId, notebookId }: { workspaceId: number; notebookId: number }) {
	const { t, colors } = useApp();
	const router = useRouter();
	const toast = useToast();
	const billingNotice = useBillingNoticeAction();
	const insets = useSafeAreaInsets();
	const sources = useSources(workspaceId, notebookId);
	const mutations = useSourceMutations(workspaceId, notebookId);
	const [adding, setAdding] = useState(false);
	const [removing, setRemoving] = useState<NotebookSource | null>(null);
	const [upload, setUpload] = useState<UploadState>({ status: "idle" });
	const [refreshing, setRefreshing] = useState(false);

	const list = sources.data ?? [];
	const allEnabled = list.length > 0 && list.every((s) => s.enabled);

	const toggle = (sourceId: number | "all", enabled: boolean) =>
		mutations.toggle.mutate(
			{ sourceId, enabled },
			{ onError: () => toast(t("notebooks.sources_update_failed"), "error") }
		);

	return (
		<View style={{ flex: 1 }}>
			<FlatList
				data={list}
				keyExtractor={(item) => String(item.id)}
				contentContainerStyle={{ paddingBottom: insets.bottom + space.xxl }}
				refreshControl={
					<RefreshControl
						refreshing={refreshing}
						onRefresh={async () => {
							setRefreshing(true);
							await sources.refetch();
							setRefreshing(false);
						}}
						tintColor={colors.mutedForeground}
					/>
				}
				ListHeaderComponent={
					<View style={styles.header}>
						<Button title={t("notebooks.add_sources")} icon="add" kind="outline" onPress={() => setAdding(true)} />
						{upload.status === "uploading" ? (
							<Notice message={t("mobile.uploading", { name: upload.name })} />
						) : upload.status === "failed" ? (
							<Notice
								tone="error"
								message={upload.message}
								onDismiss={() => setUpload({ status: "idle" })}
								{...billingNotice(upload.billing)}
							/>
						) : null}
						{sources.isError && !sources.data ? (
							<Notice
								tone="error"
								message={t("notebooks.sources_update_failed")}
								actionLabel={t("notebooks.retry")}
								onAction={() => sources.refetch()}
							/>
						) : null}
						{list.length > 0 ? (
							<View style={styles.selectAll}>
								<AppText variant="small" tone="muted" style={{ flex: 1 }}>
									{t("notebooks.select_all")}
								</AppText>
								<Checkbox
									checked={allEnabled}
									label={t("notebooks.select_all")}
									onPress={() => toggle("all", !allEnabled)}
								/>
							</View>
						) : null}
					</View>
				}
				renderItem={({ item }) => (
					<SourceRow
						source={item}
						onOpen={() =>
							router.push({
								pathname: "/notebook/[id]/source/[sourceId]",
								params: { id: String(notebookId), sourceId: String(item.id), title: item.title },
							})
						}
						onToggle={(enabled) => toggle(item.id, enabled)}
						onLongPress={() => setRemoving(item)}
					/>
				)}
				ListEmptyComponent={
					sources.isLoading ? (
						<DelayedSpinner active style={{ marginTop: space.xl }} />
					) : sources.data ? (
						<View style={styles.empty}>
							<Icon name="documents-outline" size={36} color={colors.mutedForeground} />
							<AppText variant="subheading">{t("notebooks.needs_sources.title")}</AppText>
							<AppText tone="muted" style={{ textAlign: "center" }}>
								{t("notebooks.needs_sources.add_hint")}
							</AppText>
						</View>
					) : null
				}
			/>
			<AddSourcesSheet
				visible={adding}
				onClose={() => setAdding(false)}
				workspaceId={workspaceId}
				notebookId={notebookId}
				onUploadState={setUpload}
			/>
			<Sheet
				visible={!!removing}
				onClose={() => setRemoving(null)}
				title={removing ? t("mobile.remove_source_confirm", { title: removing.title }) : undefined}
			>
				<View style={{ flexDirection: "row", gap: space.sm, justifyContent: "flex-end" }}>
					<Button kind="ghost" title={t("notebooks.cancel")} onPress={() => setRemoving(null)} />
					<Button
						kind="destructive"
						title={t("notebooks.remove_source")}
						onPress={() => {
							if (!removing) return;
							mutations.remove.mutate(removing.id, {
								onError: () => toast(t("notebooks.sources_update_failed"), "error"),
							});
							setRemoving(null);
						}}
					/>
				</View>
			</Sheet>
		</View>
	);
}

function SourceRow({
	source,
	onOpen,
	onToggle,
	onLongPress,
}: {
	source: NotebookSource;
	onOpen: () => void;
	onToggle: (enabled: boolean) => void;
	onLongPress: () => void;
}) {
	const { t, colors } = useApp();
	const indexing = isIndexing(source);
	const failed = source.status === "failed";
	return (
		<View style={styles.rowWrap}>
			{/* The switch sits beside the pressable area, not inside it, so a toggle
			    never also opens the guide. */}
			<PressableRow
				onPress={onOpen}
				onLongPress={onLongPress}
				accessibilityRole="button"
				accessibilityLabel={source.title}
				accessibilityHint={t("notebooks.source_guide")}
				style={{ flex: 1, paddingRight: space.sm }}
			>
				<Icon name={sourceIcon(source)} color={colors.mutedForeground} />
				<View style={{ flex: 1, minWidth: 0 }}>
					<AppText numberOfLines={2}>{source.title}</AppText>
					{indexing ? (
						<View style={styles.status}>
							<ActivityIndicator size="small" color={colors.mutedForeground} style={{ transform: [{ scale: 0.7 }] }} />
							<AppText variant="caption" tone="muted">
								{t("notebooks.source_indexing")}
							</AppText>
						</View>
					) : failed ? (
						<AppText variant="caption" tone="destructive">
							{t("mobile.source_failed")}
						</AppText>
					) : null}
				</View>
			</PressableRow>
			<Switch
				value={source.enabled}
				onValueChange={onToggle}
				accessibilityLabel={t("notebooks.use_source", { title: source.title })}
				trackColor={{ true: colors.primary, false: colors.border }}
				thumbColor={colors.background}
				ios_backgroundColor={colors.border}
				style={{ marginRight: space.lg }}
			/>
		</View>
	);
}

const styles = StyleSheet.create({
	header: { padding: space.lg, gap: space.md },
	rowWrap: { flexDirection: "row", alignItems: "center" },
	selectAll: { flexDirection: "row", alignItems: "center", paddingTop: space.xs, paddingRight: 6 },
	status: { flexDirection: "row", alignItems: "center", gap: 2, marginLeft: -6 },
	empty: { alignItems: "center", gap: space.sm, paddingHorizontal: space.xl, marginTop: space.xl },
});
