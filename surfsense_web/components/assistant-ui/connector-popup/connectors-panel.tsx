"use client";

import { useAtomValue } from "jotai";
import { ChevronRight, LayoutGrid, Search, X } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { activeWorkspaceIdAtom } from "@/atoms/workspaces/workspace-query.atoms";
import { Button } from "@/components/ui/button";
import {
	Drawer,
	DrawerContent,
	DrawerHandle,
	DrawerTitle,
	DrawerTrigger,
} from "@/components/ui/drawer";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { getConnectorIcon } from "@/contracts/enums/connectorIcons";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { useConnectorsSync } from "@/hooks/use-connectors-sync";
import { useEditableConnector } from "@/hooks/use-editable-connector";
import { useZeroDocumentTypeCounts } from "@/hooks/use-zero-document-type-counts";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import { cn } from "@/lib/utils";
import { ConnectorConnectView } from "./connector-configs/views/connector-connect-view";
import { ConnectorEditView } from "./connector-configs/views/connector-edit-view";
import { IndexingConfigurationView } from "./connector-configs/views/indexing-configuration-view";
import { getConnectorTitle, OAUTH_CONNECTORS } from "./constants/connector-constants";
import { useConnectorDialog } from "./hooks/use-connector-dialog";
import { type ConnectorRow, useConnectorRows } from "./hooks/use-connector-rows";
import { AllConnectorsTab } from "./tabs/all-connectors-tab";
import { ConnectorAccountsListView } from "./views/connector-accounts-list-view";
import { ConnectorRowStatus } from "./your-connectors/connector-row-status";
import {
	liveRowsChanged,
	liveRowsKey,
	workspaceConnectorList,
} from "./your-connectors/workspace-connector-list";

/**
 * Connector management surface (single `/connectors` route) rendered inside the
 * workspace panel. Stateful master–detail:
 *  - sub-rail: Overview + a flat "Your connectors" list (only what's connected:
 *    this workspace's MCP servers and every account-wide connection usable
 *    here, each with a live status glyph, "Off" where it's switched off in
 *    this workspace) + an "Add MCP server" action;
 *  - detail pane: the flat catalog (search + cards) OR one of the existing flow
 *    views (connect / edit / indexing / accounts), reused verbatim
 *    from the former dialog.
 *
 * Unconnected connectors are one click from the catalog cards, so they never
 * clutter the rail. The hook's internal `isOpen`/`connectorDialogOpenAtom` is
 * inert on a route and ignored.
 */
export function ConnectorsSection() {
	const workspaceId = useAtomValue(activeWorkspaceIdAtom);
	const documentTypeCounts = useZeroDocumentTypeCounts(workspaceId);
	const [drawerOpen, setDrawerOpen] = useState(false);

	const {
		connectingId,
		searchQuery,
		indexingConfig,
		indexingConnector,
		indexingConnectorConfig,
		editingConnector,
		connectingConnectorType,
		isCreatingConnector,
		startDate,
		endDate,
		isStartingIndexing,
		isSaving,
		isDisconnecting,
		periodicEnabled,
		frequencyMinutes,
		enableVisionLlm,
		allConnectors,
		viewingAccountsType,
		viewingMCPList,
		isFromOAuth,
		setSearchQuery,
		setPeriodicEnabled,
		setFrequencyMinutes,
		setEnableVisionLlm,
		handleOpenChange,
		handleConnectOAuth,
		handleConnectNonOAuth,
		handleCreateWebcrawler,
		handleSubmitConnectForm,
		handleStartIndexing,
		handleSkipIndexing,
		handleStartEdit,
		handleSaveConnector,
		handleDisconnectConnector,
		handleBackFromEdit,
		handleBackFromConnect,
		handleViewAccountsList,
		handleBackFromAccountsList,
		handleViewMCPList,
		handleBackFromMCPList,
		handleAddNewMCPFromList,
		handleQuickIndexConnector,
		connectorConfig,
		setConnectorConfig,
		setIndexingConnectorConfig,
		setConnectorName,
	} = useConnectorDialog();

	const { connectors: liveConnectors, loading: liveLoading } = useConnectorsSync(workspaceId);

	// The workspace's own connectors live from Zero, plus every account-wide
	// connection usable here (and its on/off state) from the REST list.
	const connectors = useMemo(
		() =>
			workspaceConnectorList(
				liveLoading ? [] : liveConnectors,
				allConnectors as SearchSourceConnector[] | undefined
			),
		[liveLoading, liveConnectors, allConnectors]
	);

	// Account-wide connections reach this list only through the REST list.
	const refreshConnectors = async () => {
		if (!workspaceId) return;
		await queryClient.invalidateQueries({ queryKey: cacheKeys.connectors.all(workspaceId) });
	};

	// An MCP server added or removed elsewhere (another tab, a teammate)
	// reaches Zero at once but the REST list only when refetched; without
	// this, the last server removed stayed listed (workspaceConnectorList
	// falls back to the REST list when Zero holds no rows).
	const liveKeyRef = useRef<{ workspaceId: string; key: string } | null>(null);
	useEffect(() => {
		if (!workspaceId || liveLoading) return;
		const previous = liveKeyRef.current;
		const previousKey = previous?.workspaceId === workspaceId ? previous.key : null;
		if (liveRowsChanged(previousKey, liveConnectors)) {
			void queryClient.invalidateQueries({ queryKey: cacheKeys.connectors.all(workspaceId) });
		}
		liveKeyRef.current = { workspaceId, key: liveRowsKey(liveConnectors) };
	}, [workspaceId, liveLoading, liveConnectors]);

	// "Your connectors" rows with live indexing health, plus the shared indexing
	// controls the flow views reuse (single source of truth via useConnectorRows).
	const {
		rows: connectedRows,
		indexingConnectorIds,
		startIndexing,
		stopIndexing,
	} = useConnectorRows(connectors);

	const connectedTypes = useMemo(
		() => new Set<string>(connectors.map((c) => c.connector_type)),
		[connectors]
	);

	// An MCP server's edit view shows its config as the server holds it now,
	// asked for when the view opens (editable-connector.ts).
	const editable = useEditableConnector(editingConnector);

	if (!workspaceId) return null;

	const activeConnectorType = editingConnector
		? editingConnector.connector_type
		: connectingConnectorType ||
			viewingAccountsType?.connectorType ||
			(viewingMCPList ? EnumConnectorName.MCP_CONNECTOR : null);

	const flowView = ((): ReactNode => {
		if (viewingMCPList) {
			return (
				<ConnectorAccountsListView
					connectorType="MCP_CONNECTOR"
					connectorTitle="MCP Connectors"
					connectors={(allConnectors || []) as SearchSourceConnector[]}
					indexingConnectorIds={indexingConnectorIds}
					onBack={handleBackFromMCPList}
					onManage={handleStartEdit}
					onAddAccount={handleAddNewMCPFromList}
					addButtonText="Add New MCP Server"
				/>
			);
		}
		if (viewingAccountsType) {
			return (
				<ConnectorAccountsListView
					connectorType={viewingAccountsType.connectorType}
					connectorTitle={viewingAccountsType.connectorTitle}
					connectors={connectors}
					indexingConnectorIds={indexingConnectorIds}
					onBack={handleBackFromAccountsList}
					onManage={handleStartEdit}
					// Only an OAuth type adds an account from here. Email (IMAP)
					// mailboxes are added on its card; Nextcloud is one per person.
					onAddAccount={(() => {
						const oauthConnector = OAUTH_CONNECTORS.find(
							(c) => c.connectorType === viewingAccountsType.connectorType
						);
						return oauthConnector ? () => handleConnectOAuth(oauthConnector) : undefined;
					})()}
					isConnecting={connectingId !== null}
				/>
			);
		}
		if (connectingConnectorType) {
			return (
				<ConnectorConnectView
					connectorType={connectingConnectorType}
					onSubmit={(formData) => handleSubmitConnectForm(formData, startIndexing)}
					onBack={handleBackFromConnect}
					isSubmitting={isCreatingConnector}
				/>
			);
		}
		if (editingConnector && editable) {
			return (
				<ConnectorEditView
					connector={{
						...editable.connector,
						config: connectorConfig || editable.connector.config,
						name: editingConnector.name,
						last_indexed_at:
							connectors.find((c) => c.id === editingConnector.id)?.last_indexed_at ??
							editingConnector.last_indexed_at,
					}}
					periodicEnabled={periodicEnabled}
					frequencyMinutes={frequencyMinutes}
					enableVisionLlm={enableVisionLlm}
					isSaving={isSaving}
					isDisconnecting={isDisconnecting}
					isIndexing={indexingConnectorIds.has(editingConnector.id)}
					configPending={editable.pending}
					workspaceId={workspaceId?.toString()}
					onPeriodicEnabledChange={setPeriodicEnabled}
					onFrequencyChange={setFrequencyMinutes}
					onEnableVisionLlmChange={setEnableVisionLlm}
					onSave={() => handleSaveConnector(() => refreshConnectors(), startIndexing)}
					onDisconnect={() => handleDisconnectConnector(() => refreshConnectors())}
					onBack={handleBackFromEdit}
					onQuickIndex={(() => {
						const cfg = connectorConfig || editingConnector.config;
						const isDrive = editingConnector.connector_type === "GOOGLE_DRIVE_CONNECTOR";
						const hasDriveItems = isDrive
							? ((cfg?.selected_folders as unknown[]) ?? []).length > 0 ||
								((cfg?.selected_files as unknown[]) ?? []).length > 0
							: true;
						if (!hasDriveItems) return undefined;
						return () => {
							startIndexing(editingConnector.id);
							handleQuickIndexConnector(
								editingConnector.id,
								editingConnector.connector_type,
								stopIndexing,
								startDate,
								endDate
							);
						};
					})()}
					onConfigChange={setConnectorConfig}
					onNameChange={setConnectorName}
				/>
			);
		}
		if (indexingConfig) {
			return (
				<IndexingConfigurationView
					config={indexingConfig}
					connector={
						indexingConnector
							? {
									...indexingConnector,
									config: indexingConnectorConfig || indexingConnector.config,
								}
							: undefined
					}
					periodicEnabled={periodicEnabled}
					frequencyMinutes={frequencyMinutes}
					enableVisionLlm={enableVisionLlm}
					isStartingIndexing={isStartingIndexing}
					isFromOAuth={isFromOAuth}
					onPeriodicEnabledChange={setPeriodicEnabled}
					onFrequencyChange={setFrequencyMinutes}
					onEnableVisionLlmChange={setEnableVisionLlm}
					onConfigChange={setIndexingConnectorConfig}
					onStartIndexing={() => {
						if (indexingConfig.connectorId) {
							startIndexing(indexingConfig.connectorId);
						}
						handleStartIndexing(() => refreshConnectors());
					}}
					onSkip={handleSkipIndexing}
				/>
			);
		}
		return null;
	})();

	const isFlowActive = flowView !== null;

	const detailTitle =
		isFlowActive && activeConnectorType ? getConnectorTitle(activeConnectorType) : "Overview";

	const hasConnected = connectedRows.length > 0;

	// Rail navigation: clear any open flow first (handleOpenChange(false) is the
	// hook's full reset), then apply the target management action.
	const resetFlow = () => handleOpenChange(false);
	const goOverview = () => {
		setDrawerOpen(false);
		resetFlow();
	};
	const manageRow = (row: ConnectorRow) => {
		setDrawerOpen(false);
		resetFlow();
		if (row.type === EnumConnectorName.MCP_CONNECTOR) {
			handleViewMCPList();
			return;
		}
		if (row.accountCount > 1) {
			handleViewAccountsList(row.type, row.title);
			return;
		}
		handleStartEdit(row.connectors[0]);
	};
	const addMcpServer = () => {
		setDrawerOpen(false);
		resetFlow();
		handleConnectNonOAuth?.(EnumConnectorName.MCP_CONNECTOR);
	};

	const navBtnClass = (active: boolean) =>
		cn(
			"inline-flex w-full items-center justify-start gap-3 rounded-md px-3 py-2.5 text-left text-sm font-medium transition-colors duration-150 focus:outline-none",
			active
				? "bg-accent text-accent-foreground"
				: "text-muted-foreground hover:bg-accent hover:text-accent-foreground"
		);

	const railLabel = (text: string) => (
		<p className="px-3 pb-1 pt-1 text-xs font-semibold text-muted-foreground">{text}</p>
	);

	const renderNav = () => (
		<>
			<button type="button" onClick={goOverview} className={navBtnClass(!isFlowActive)}>
				<LayoutGrid className="h-4 w-4" />
				<span className="min-w-0 truncate">Overview</span>
			</button>

			{hasConnected && (
				<>
					<Separator className="my-3 bg-border" />
					{railLabel("Your connectors")}
					<div className="flex flex-col gap-0.5">
						{connectedRows.map((row) => {
							const isActive = isFlowActive && activeConnectorType === row.type;
							return (
								<button
									key={row.type}
									type="button"
									onClick={() => manageRow(row)}
									className={cn(
										"inline-flex w-full items-center justify-start gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors duration-150 focus:outline-none",
										isActive
											? "bg-accent text-accent-foreground"
											: "text-muted-foreground hover:bg-accent hover:text-accent-foreground"
									)}
								>
									<span className="flex size-4 shrink-0 items-center justify-center">
										{getConnectorIcon(row.type, "size-4")}
									</span>
									<span className="min-w-0 truncate">{row.title}</span>
									<ConnectorRowStatus row={row} />
								</button>
							);
						})}
					</div>
				</>
			)}

			<Separator className="my-3 bg-border" />
			<button type="button" onClick={addMcpServer} className={navBtnClass(false)}>
				{getConnectorIcon(EnumConnectorName.MCP_CONNECTOR, "h-4 w-4")}
				<span className="min-w-0 truncate">Add MCP server</span>
			</button>
		</>
	);

	const catalog = (
		<div className="flex flex-col gap-8">
			<div className="w-full">
				<div className="relative">
					<Search className="absolute left-3.5 top-1/2 -translate-y-1/2 h-4.5 w-4.5 text-muted-foreground" />
					<Input
						type="text"
						autoComplete="off"
						placeholder="Search connectors"
						className="h-10 border-0 bg-muted pl-10 pr-9 text-base shadow-none"
						value={searchQuery}
						onChange={(e) => setSearchQuery(e.target.value)}
					/>
					{searchQuery && (
						<Button
							variant="ghost"
							size="icon"
							type="button"
							onClick={() => setSearchQuery("")}
							className="absolute right-2 top-1/2 h-7 w-7 -translate-y-1/2 rounded-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
							aria-label="Clear search"
						>
							<X className="h-4.5 w-4.5" />
						</Button>
					)}
				</div>
			</div>

			<AllConnectorsTab
				searchQuery={searchQuery}
				workspaceId={workspaceId}
				connectedTypes={connectedTypes}
				connectingId={connectingId}
				allConnectors={connectors}
				documentTypeCounts={documentTypeCounts}
				indexingConnectorIds={indexingConnectorIds}
				onConnectNonOAuth={handleConnectNonOAuth}
				onCreateWebcrawler={handleCreateWebcrawler}
				onManage={handleStartEdit}
			/>
		</div>
	);

	return (
		<section className="flex h-full min-h-[min(680px,calc(100vh-5rem))] w-full select-none flex-col gap-6 md:flex-row">
			<div className="md:w-[220px] md:shrink-0">
				<h1 className="mb-4 px-1 text-xl font-semibold tracking-tight text-foreground md:text-2xl">
					Connectors
				</h1>
				<nav className="hidden flex-col gap-0.5 md:flex">{renderNav()}</nav>
				<Drawer open={drawerOpen} onOpenChange={setDrawerOpen} shouldScaleBackground={false}>
					<DrawerTrigger asChild>
						<Button
							type="button"
							variant="outline"
							className="flex h-10 w-full justify-between bg-transparent px-3 hover:bg-accent md:hidden"
						>
							<span className="truncate">{detailTitle}</span>
							<ChevronRight className="h-4 w-4 rotate-90 text-muted-foreground" />
						</Button>
					</DrawerTrigger>
					<DrawerContent className="h-[88vh] overflow-hidden rounded-t-2xl border bg-popover text-popover-foreground">
						<DrawerHandle className="mt-3 h-1.5 w-10" />
						<DrawerTitle className="sr-only">Connectors navigation</DrawerTitle>
						<nav className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto p-4">
							{renderNav()}
						</nav>
					</DrawerContent>
				</Drawer>
			</div>

			<div className="min-w-0 flex-1">
				<div className="hidden md:block">
					<h2 className="text-lg font-semibold">{detailTitle}</h2>
					<Separator className="mt-4 bg-border" />
				</div>
				<div className="min-w-0 pt-4 md:max-w-5xl">{isFlowActive ? flowView : catalog}</div>
			</div>
		</section>
	);
}
