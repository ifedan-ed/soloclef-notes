import { useLocalSearchParams, useRouter } from "expo-router";
import { ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { MarkdownView } from "../../../../components/markdown-view";
import { Header, Screen } from "../../../../components/ui/layout";
import { AppText, DelayedSpinner, Notice, useDelayedFlag } from "../../../../components/ui/primitives";
import { useApp } from "../../../../lib/app-context";
import { useSourceGuide, useSources, useWorkspace } from "../../../../lib/queries";
import { space } from "../../../../theme/colors";

/** A source's guide: the summary the backend writes for each source (web: Source guide). */
export default function SourceGuideScreen() {
	const params = useLocalSearchParams<{ id: string; sourceId: string; title?: string }>();
	const notebookId = Number(params.id);
	const sourceId = Number(params.sourceId);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { t } = useApp();
	const { workspaceId } = useWorkspace();
	const sources = useSources(workspaceId, notebookId);
	const guide = useSourceGuide(workspaceId, notebookId, sourceId);
	const source = sources.data?.find((s) => s.id === sourceId);
	const title = guide.data?.title ?? source?.title ?? params.title ?? t("notebooks.source_guide");
	const slow = useDelayedFlag(guide.isLoading);

	return (
		<Screen>
			<Header
				onBack={() => router.back()}
				backLabel={t("notebooks.back_to_sources")}
				title={title}
				subtitle={t("notebooks.source_guide")}
			/>
			<ScrollView
				contentContainerStyle={{ padding: space.lg, paddingBottom: insets.bottom + space.xxl, gap: space.md }}
			>
				{guide.isLoading ? (
					slow ? (
						<View style={{ alignItems: "center", gap: space.sm, marginTop: space.xl }}>
							<DelayedSpinner active />
							<AppText tone="muted">{t("notebooks.source_guide_generating")}</AppText>
						</View>
					) : null
				) : guide.isError ? (
					<Notice
						tone="error"
						message={t("notebooks.source_guide_unavailable")}
						actionLabel={t("notebooks.retry")}
						onAction={() => guide.refetch()}
					/>
				) : guide.data?.status === "ready" && guide.data.summary ? (
					<MarkdownView text={guide.data.summary} />
				) : (
					<Notice message={t("notebooks.source_guide_unavailable")} />
				)}
			</ScrollView>
		</Screen>
	);
}
