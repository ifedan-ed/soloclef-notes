import { useRouter } from "expo-router";
import { useState } from "react";
import { ActivityIndicator, FlatList, Pressable, RefreshControl, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { StudioKind, StudioOutput } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { useOutputs, useSources, useStudioMutations } from "../../lib/queries";
import { formatDuration, isInFlight, knownPhase, STUDIO_TILES } from "../../lib/studio/catalog";
import { newRequestId } from "../../lib/studio/request";
import { relativeTime } from "../../lib/util/notebooks";
import { radius, space } from "../../theme/colors";
import { useToast } from "../ui/layout";
import { AppText, DelayedSpinner, Icon, IconButton, Notice } from "../ui/primitives";
import { GenerateSheet } from "./generate-sheet";
import { OutputActionsSheet } from "./output-actions";
import { tileIcon } from "./tile-icon";

export function StudioPane({
	workspaceId,
	notebookId,
	notebookTitle,
	onAddSources,
}: {
	workspaceId: number;
	notebookId: number;
	notebookTitle: string;
	onAddSources: () => void;
}) {
	const { t, colors } = useApp();
	const router = useRouter();
	const toast = useToast();
	const insets = useSafeAreaInsets();
	const outputs = useOutputs(workspaceId, notebookId);
	const sources = useSources(workspaceId, notebookId);
	const studio = useStudioMutations(workspaceId, notebookId);
	const [dialogKind, setDialogKind] = useState<StudioKind | null>(null);
	const [actionsFor, setActionsFor] = useState<StudioOutput | null>(null);
	const [refreshing, setRefreshing] = useState(false);

	const open = (output: StudioOutput) => {
		if (output.status !== "ready") return;
		router.push({
			pathname: "/notebook/[id]/output/[outputId]",
			params: { id: String(notebookId), outputId: String(output.id) },
		});
	};

	const retry = (output: StudioOutput) =>
		studio.generate.mutate(
			{ ...output.options, kind: output.kind, title: output.title, request_id: newRequestId() },
			{
				onSuccess: () => toast(t("notebooks.generation_started"), "success"),
				onError: () => toast(t("notebooks.generate_failed"), "error"),
			}
		);

	const list = outputs.data ?? [];

	return (
		<View style={{ flex: 1 }}>
			<FlatList
				data={list}
				keyExtractor={(o) => String(o.id)}
				contentContainerStyle={{ paddingBottom: insets.bottom + space.xxl }}
				refreshControl={
					<RefreshControl
						refreshing={refreshing}
						onRefresh={async () => {
							setRefreshing(true);
							await outputs.refetch();
							setRefreshing(false);
						}}
						tintColor={colors.mutedForeground}
					/>
				}
				ListHeaderComponent={
					<View style={styles.header}>
						<View style={styles.grid}>
							{STUDIO_TILES.map((tile) => (
								<Pressable
									key={tile.kind}
									onPress={() => setDialogKind(tile.kind)}
									accessibilityRole="button"
									accessibilityLabel={t(`notebooks.kinds.${tile.kind}.label`)}
									accessibilityHint={t(`notebooks.kinds.${tile.kind}.tooltip`)}
									style={({ pressed }) => [
										styles.tile,
										{ borderColor: colors.border, backgroundColor: pressed ? colors.accent : colors.muted },
									]}
								>
									<Icon name={tileIcon(tile.kind)} size={20} />
									<AppText variant="small" numberOfLines={2} style={{ fontWeight: "600" }}>
										{t(`notebooks.kinds.${tile.kind}.label`)}
									</AppText>
								</Pressable>
							))}
						</View>
						{outputs.isError && !outputs.data ? (
							<Notice
								tone="error"
								message={t("mobile.output_failed")}
								actionLabel={t("notebooks.retry")}
								onAction={() => outputs.refetch()}
							/>
						) : null}
						{list.length > 0 ? (
							<AppText variant="label" tone="muted">
								{t("mobile.studio_outputs")}
							</AppText>
						) : null}
					</View>
				}
				renderItem={({ item }) => (
					<OutputRow output={item} onOpen={() => open(item)} onMore={() => setActionsFor(item)} />
				)}
				ListEmptyComponent={
					outputs.isLoading ? (
						<DelayedSpinner active style={{ marginTop: space.lg }} />
					) : outputs.data ? (
						<View style={styles.empty}>
							<Icon name="sparkles-outline" size={32} color={colors.mutedForeground} />
							<AppText variant="subheading">{t("notebooks.studio_empty_title")}</AppText>
							<AppText tone="muted" style={{ textAlign: "center" }}>
								{t("notebooks.studio_empty")}
							</AppText>
						</View>
					) : null
				}
			/>
			<GenerateSheet
				kind={dialogKind}
				onClose={() => setDialogKind(null)}
				workspaceId={workspaceId}
				notebookId={notebookId}
				notebookTitle={notebookTitle}
				sources={sources.data ?? []}
				onAddSources={() => {
					setDialogKind(null);
					onAddSources();
				}}
				onGenerate={async (body) => {
					await studio.generate.mutateAsync(body);
					toast(t("notebooks.generation_started"), "success");
				}}
			/>
			<OutputActionsSheet
				output={actionsFor}
				onClose={() => setActionsFor(null)}
				onOpen={() => actionsFor && open(actionsFor)}
				onRetry={() => actionsFor && retry(actionsFor)}
				onRename={async (title) => {
					if (actionsFor) await studio.rename.mutateAsync({ outputId: actionsFor.id, title });
				}}
				onDelete={async () => {
					if (actionsFor) await studio.remove.mutateAsync(actionsFor.id);
				}}
			/>
		</View>
	);
}

/** Status line, as the web's StudioOutputRow words it. */
export function outputStatus(
	output: StudioOutput,
	t: (key: string, values?: Record<string, string | number>) => string
): string | null {
	if (isInFlight(output.status)) {
		const phase = output.status === "running" ? knownPhase(output.phase) : null;
		if (phase) {
			return output.progress
				? t("notebooks.phase_progress", { phase: t(`notebooks.phases.${phase}`), progress: output.progress })
				: t(`notebooks.phases.${phase}`);
		}
		return output.progress
			? t("notebooks.status_progress", { progress: output.progress })
			: t(`notebooks.status.${output.status}`);
	}
	return output.status === "ready" ? null : t(`notebooks.status.${output.status}`);
}

function OutputRow({ output, onOpen, onMore }: { output: StudioOutput; onOpen: () => void; onMore: () => void }) {
	const { t, locale, colors } = useApp();
	const inFlight = isInFlight(output.status);
	const status = outputStatus(output, t);
	const format = output.options.format
		? t(`notebooks.options.${output.kind}.format.${String(output.options.format)}.label`)
		: output.kind === "infographic" && output.options.style
			? t(`notebooks.options.infographic.style.${String(output.options.style)}.label`)
			: t(`notebooks.kinds.${output.kind}.label`);
	const meta = [
		output.duration_seconds ? formatDuration(output.duration_seconds) : null,
		format,
		t("notebooks.source_count", { count: output.source_count }),
		relativeTime(output.created_at, locale),
	]
		.filter(Boolean)
		.join(" · ");
	const playable = output.status === "ready" && (output.kind === "audio" || output.kind === "video");

	return (
		<View style={styles.rowWrap}>
			<Pressable
				onPress={output.status === "ready" ? onOpen : onMore}
				onLongPress={onMore}
				accessibilityRole="button"
				accessibilityLabel={`${output.title}. ${status ?? ""} ${meta}`}
				style={({ pressed }) => [styles.row, { backgroundColor: pressed ? colors.accent : "transparent" }]}
			>
				<View style={[styles.rowIcon, { backgroundColor: colors.muted }]}>
					{inFlight ? (
						<ActivityIndicator size="small" color={colors.mutedForeground} />
					) : (
						<Icon name={tileIcon(output.kind)} size={18} color={colors.mutedForeground} />
					)}
				</View>
				<View style={{ flex: 1, minWidth: 0 }}>
					<AppText numberOfLines={2} style={{ fontWeight: "600" }}>
						{output.title}
					</AppText>
					<AppText variant="caption" tone="muted" numberOfLines={2}>
						{status ? (
							<AppText variant="caption" tone={output.status === "failed" ? "destructive" : "muted"}>
								{`${status} · `}
							</AppText>
						) : null}
						{meta}
					</AppText>
				</View>
			</Pressable>
			{playable ? <IconButton name="play" label={t("notebooks.play")} onPress={onOpen} /> : null}
			<IconButton
				name="ellipsis-vertical"
				label={t("notebooks.more_actions")}
				onPress={onMore}
				color={colors.mutedForeground}
				style={{ marginRight: 8 }}
			/>
		</View>
	);
}

const styles = StyleSheet.create({
	header: { padding: space.lg, gap: space.md },
	grid: { flexDirection: "row", flexWrap: "wrap", gap: space.sm },
	tile: {
		width: "31.5%",
		flexGrow: 1,
		minHeight: 72,
		borderWidth: 1,
		borderRadius: radius.md,
		padding: space.sm,
		gap: 6,
		justifyContent: "space-between",
	},
	rowWrap: { flexDirection: "row", alignItems: "center" },
	row: {
		flex: 1,
		flexDirection: "row",
		alignItems: "center",
		gap: space.md,
		paddingLeft: space.lg,
		paddingRight: space.sm,
		paddingVertical: space.sm,
		minHeight: 56,
	},
	rowIcon: { width: 36, height: 36, borderRadius: radius.md, alignItems: "center", justifyContent: "center" },
	empty: { alignItems: "center", gap: space.sm, paddingHorizontal: space.xl, marginTop: space.lg },
});
