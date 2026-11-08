import { useLocalSearchParams, useRouter } from "expo-router";
import { useState } from "react";
import { Pressable, StyleSheet, View } from "react-native";
import { ChatPane } from "../../../components/chat/chat-pane";
import { NotebookActionsSheet } from "../../../components/notebooks/notebook-actions";
import { SourcesPane } from "../../../components/sources/sources-pane";
import { StudioPane } from "../../../components/studio/studio-pane";
import { Header, Screen } from "../../../components/ui/layout";
import { AppText, IconButton, Notice } from "../../../components/ui/primitives";
import { useApp } from "../../../lib/app-context";
import { useNotebook, useNotebookMutations, useSources, useWorkspace } from "../../../lib/queries";
import { radius, space } from "../../../theme/colors";

type Tab = "sources" | "chat" | "studio";

/**
 * One notebook, laid out like the web's phone view: a header, then
 * Sources / Chat / Studio pills. All three panes stay mounted so a streaming
 * answer or an upload keeps going while another tab is shown.
 */
export default function NotebookScreen() {
	const { id } = useLocalSearchParams<{ id: string }>();
	const notebookId = Number(id);
	const router = useRouter();
	const { t, colors } = useApp();
	const { workspaceId } = useWorkspace();
	const notebook = useNotebook(workspaceId, notebookId);
	const sources = useSources(workspaceId, notebookId);
	const mutations = useNotebookMutations(workspaceId);
	const [tab, setTab] = useState<Tab>("chat");
	const [menu, setMenu] = useState<"menu" | "rename" | null>(null);

	const enabled = (sources.data ?? []).filter((s) => s.enabled).length;
	const data = notebook.data;
	const back = () => (router.canGoBack() ? router.back() : router.replace("/"));

	return (
		<Screen>
			<Header
				onBack={back}
				backLabel={t("notebooks.back_to_notebooks")}
				title={
					<Pressable
						onPress={() => data && setMenu("rename")}
						accessibilityRole="button"
						accessibilityLabel={`${data?.title ?? ""}. ${t("notebooks.rename")}`}
						style={styles.titleRow}
					>
						<AppText style={{ fontSize: 20 }}>{data?.icon || "📓"}</AppText>
						<AppText variant="subheading" numberOfLines={1} style={{ flex: 1 }}>
							{data?.title ?? ""}
						</AppText>
					</Pressable>
				}
				right={
					<IconButton
						name="ellipsis-horizontal"
						label={t("notebooks.notebook_menu")}
						onPress={() => data && setMenu("menu")}
					/>
				}
			/>
			<View accessibilityRole="tablist" style={[styles.tabs, { borderBottomColor: colors.border }]}>
				{(["sources", "chat", "studio"] as const).map((key) => {
					const selected = tab === key;
					return (
						<Pressable
							key={key}
							accessibilityRole="tab"
							accessibilityState={{ selected }}
							onPress={() => setTab(key)}
							style={[styles.tab, { backgroundColor: selected ? colors.primary : "transparent" }]}
						>
							<AppText
								variant="small"
								numberOfLines={1}
								style={{ fontWeight: "600", color: selected ? colors.primaryForeground : colors.mutedForeground }}
							>
								{key === "sources" ? `${t("notebooks.sources")} (${enabled})` : t(`notebooks.${key}`)}
							</AppText>
						</Pressable>
					);
				})}
			</View>
			{notebook.isError && !data ? (
				<View style={{ padding: space.lg }}>
					<Notice
						tone="error"
						message={t("notebooks.notebook_not_found")}
						actionLabel={t("notebooks.retry")}
						onAction={() => notebook.refetch()}
					/>
				</View>
			) : null}
			<View style={{ flex: 1 }}>
				<View style={[styles.pane, tab !== "sources" && styles.hidden]}>
					<SourcesPane workspaceId={workspaceId} notebookId={notebookId} />
				</View>
				<View style={[styles.pane, tab !== "chat" && styles.hidden]}>
					<ChatPane
						workspaceId={workspaceId}
						notebookId={notebookId}
						notebook={data}
						onAddSources={() => setTab("sources")}
					/>
				</View>
				<View style={[styles.pane, tab !== "studio" && styles.hidden]}>
					<StudioPane
						workspaceId={workspaceId}
						notebookId={notebookId}
						notebookTitle={data?.title ?? ""}
						onAddSources={() => setTab("sources")}
					/>
				</View>
			</View>
			<NotebookActionsSheet
				notebook={menu && data ? data : null}
				initialMode={menu ?? "menu"}
				onClose={() => setMenu(null)}
				onRename={async (title) => {
					await mutations.rename.mutateAsync({ id: notebookId, title });
				}}
				onDelete={async () => {
					await mutations.remove.mutateAsync(notebookId);
					router.replace("/");
				}}
			/>
		</Screen>
	);
}

const styles = StyleSheet.create({
	titleRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
	tabs: {
		flexDirection: "row",
		gap: space.xs,
		paddingHorizontal: space.sm,
		paddingVertical: 6,
		borderBottomWidth: StyleSheet.hairlineWidth,
	},
	tab: {
		flex: 1,
		minHeight: 36,
		borderRadius: radius.pill,
		alignItems: "center",
		justifyContent: "center",
		paddingHorizontal: space.sm,
	},
	pane: { position: "absolute", top: 0, left: 0, right: 0, bottom: 0 },
	hidden: { display: "none" },
});
