import { useRouter } from "expo-router";
import { useMemo, useState } from "react";
import { FlatList, RefreshControl, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ContentSearchResults } from "../components/notebooks/content-search-results";
import { NotebookActionsSheet } from "../components/notebooks/notebook-actions";
import { NotebookRow } from "../components/notebooks/notebook-row";
import { Header, Screen, useToast } from "../components/ui/layout";
import { AppText, Button, DelayedSpinner, IconButton, Notice, Segmented, TextField } from "../components/ui/primitives";
import type { Notebook, SearchPassage } from "../lib/api/schemas";
import { useApp } from "../lib/app-context";
import { useContentSearch, useNotebookMutations, useNotebooks, useWorkspace } from "../lib/queries";
import { defaultNotebookIcon, type HomeTab, homeSections } from "../lib/util/notebooks";
import { space } from "../theme/colors";

export default function NotebooksHome() {
	const { t, colors } = useApp();
	const router = useRouter();
	const toast = useToast();
	const insets = useSafeAreaInsets();
	const { workspaceId, workspace, query: workspaces } = useWorkspace();
	const notebooks = useNotebooks(workspaceId);
	const mutations = useNotebookMutations(workspaceId);
	const [tab, setTab] = useState<HomeTab>("all");
	const [query, setQuery] = useState("");
	const [actionsFor, setActionsFor] = useState<Notebook | null>(null);
	const [refreshing, setRefreshing] = useState(false);
	const inside = useContentSearch(workspaceId, query);

	const sections = useMemo(() => homeSections(notebooks.data ?? [], tab, query), [notebooks.data, tab, query]);
	// Featured notebooks lead "All"; the rest follow without repeating them.
	const rows = useMemo(() => {
		const featuredIds = new Set(sections.featured.map((n) => n.id));
		return [...sections.featured, ...sections.notebooks.filter((n) => !featuredIds.has(n.id))];
	}, [sections]);

	const open = (notebook: Pick<Notebook, "id">) =>
		router.push({ pathname: "/notebook/[id]", params: { id: String(notebook.id) } });
	// A document source opens on its guide; a note, or a document inside a
	// source folder, opens its notebook (the app has no note or folder view).
	const openPassage = (notebookId: number, passage: SearchPassage) => {
		if (passage.kind === "source" && passage.source_id && !passage.folder_name) {
			router.push({
				pathname: "/notebook/[id]/source/[sourceId]",
				params: { id: String(notebookId), sourceId: String(passage.source_id), title: passage.title },
			});
		} else {
			open({ id: notebookId });
		}
	};

	const create = async () => {
		if (mutations.create.isPending) return;
		try {
			const notebook = await mutations.create.mutateAsync({
				title: t("notebooks.untitled"),
				icon: defaultNotebookIcon(Date.now()),
			});
			open(notebook);
		} catch {
			toast(t("notebooks.create_failed"), "error");
		}
	};

	const refresh = async () => {
		setRefreshing(true);
		await Promise.allSettled([notebooks.refetch(), workspaces.refetch()]);
		setRefreshing(false);
	};

	const loading = (workspaces.isLoading || notebooks.isLoading) && !notebooks.data;
	const noWorkspace = workspaces.isSuccess && !workspace;

	return (
		<Screen>
			<Header
				title={t("notebooks.home_title")}
				subtitle={workspace?.name}
				right={
					<IconButton
						name="settings-outline"
						label={t("notebooks.settings")}
						onPress={() => router.push("/settings")}
					/>
				}
			/>
			<FlatList
				data={rows}
				keyExtractor={(item) => String(item.id)}
				keyboardShouldPersistTaps="handled"
				contentContainerStyle={{ paddingBottom: insets.bottom + space.xxl }}
				refreshControl={
					<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={colors.mutedForeground} />
				}
				ListHeaderComponent={
					<View style={styles.controls}>
						<Segmented
							accessibilityLabel={t("notebooks.home_filter")}
							value={tab}
							onChange={setTab}
							options={[
								{ value: "all", label: t("notebooks.tab_all") },
								{ value: "recent", label: t("notebooks.tab_recent") },
							]}
						/>
						<TextField
							value={query}
							onChangeText={setQuery}
							placeholder={t("notebooks.search_notebooks")}
							accessibilityLabel={t("notebooks.search_notebooks")}
							clearButtonMode="while-editing"
							returnKeyType="search"
						/>
						<Button
							title={t("notebooks.new_notebook")}
							icon="add"
							onPress={create}
							busy={mutations.create.isPending}
							disabled={workspaceId <= 0}
						/>
						{noWorkspace ? <Notice message={t("mobile.no_workspace")} /> : null}
						{notebooks.isError && !notebooks.data ? (
							<Notice
								tone="error"
								message={t("notebooks.load_failed")}
								actionLabel={t("notebooks.retry")}
								onAction={() => notebooks.refetch()}
							/>
						) : null}
						{sections.featured.length > 0 && tab === "all" && !query ? (
							<AppText variant="label" tone="muted" style={{ marginTop: space.sm }}>
								{t("notebooks.featured")}
							</AppText>
						) : null}
					</View>
				}
				renderItem={({ item, index }) => (
					<>
						{tab === "all" && !query && sections.featured.length > 0 && index === sections.featured.length ? (
							<AppText variant="label" tone="muted" style={styles.sectionLabel}>
								{t("notebooks.title")}
							</AppText>
						) : null}
						<NotebookRow notebook={item} onPress={() => open(item)} onMore={() => setActionsFor(item)} />
					</>
				)}
				ListFooterComponent={
					inside.active ? (
						<ContentSearchResults
							query={inside.query}
							hits={inside.hits}
							pending={inside.pending}
							settled={inside.settled}
							failed={inside.failed}
							onOpenNotebook={(id) => open({ id })}
							onOpenPassage={openPassage}
						/>
					) : null
				}
				ListEmptyComponent={
					loading ? (
						<DelayedSpinner active style={{ marginTop: space.xxl }} />
					) : notebooks.data ? (
						<AppText tone="muted" style={styles.empty}>
							{query ? t("notebooks.no_matches", { query }) : t("notebooks.empty_hint")}
						</AppText>
					) : null
				}
			/>
			<NotebookActionsSheet
				notebook={actionsFor}
				onClose={() => setActionsFor(null)}
				onRename={async (title) => {
					if (!actionsFor) return;
					await mutations.rename.mutateAsync({ id: actionsFor.id, title });
				}}
				onDelete={async () => {
					if (!actionsFor) return;
					await mutations.remove.mutateAsync(actionsFor.id);
				}}
			/>
		</Screen>
	);
}

const styles = StyleSheet.create({
	controls: { padding: space.lg, gap: space.md },
	sectionLabel: { paddingHorizontal: space.lg, marginTop: space.lg, marginBottom: space.xs },
	empty: { textAlign: "center", paddingHorizontal: space.xl, marginTop: space.xl },
});
