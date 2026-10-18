"use client";

import { useAtomValue } from "jotai";
import { ArrowLeft, Info, RefreshCw } from "lucide-react";
import { type FC, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { setConnectorEnabledMutationAtom } from "@/atoms/connectors/connector-mutation.atoms";
import { activeWorkspaceIdAtom } from "@/atoms/workspaces/workspace-query.atoms";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { getConnectorIcon } from "@/contracts/enums/connectorIcons";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { authenticatedFetch } from "@/lib/auth-fetch";
import { BRAND } from "@/lib/brand";
import { getReauthEndpoint } from "@/lib/connector-telemetry";
import { buildBackendUrl } from "@/lib/env-config";
import { cn } from "@/lib/utils";
import { PeriodicSyncConfig } from "../../components/periodic-sync-config";
import { VisionLLMConfig } from "../../components/vision-llm-config";
import { getConnectorDisplayName } from "../../tabs/all-connectors-tab";
import { EnabledInWorkspaceSwitch } from "../../workspace-enablement/enabled-in-workspace-switch";
import { MCPServiceConfig } from "../components/mcp-service-config";
import { getConnectorConfigComponent } from "../index";
import { editViewActions } from "./edit-view-actions";

const VISION_LLM_CONNECTOR_TYPES = new Set<SearchSourceConnector["connector_type"]>([
	EnumConnectorName.GOOGLE_DRIVE_CONNECTOR,
]);

interface ConnectorEditViewProps {
	connector: SearchSourceConnector;
	periodicEnabled: boolean;
	frequencyMinutes: string;
	enableVisionLlm: boolean;
	isSaving: boolean;
	isDisconnecting: boolean;
	isIndexing?: boolean;
	/** The server's copy of an MCP server's config is still on its way (editable-connector.ts). */
	configPending?: boolean;
	workspaceId?: string;
	onPeriodicEnabledChange: (enabled: boolean) => void;
	onFrequencyChange: (frequency: string) => void;
	onEnableVisionLlmChange: (enabled: boolean) => void;
	onSave: () => void;
	onDisconnect: () => void;
	onBack: () => void;
	onQuickIndex?: () => void;
	onConfigChange?: (config: Record<string, unknown>) => void;
	onNameChange?: (name: string) => void;
}

export const ConnectorEditView: FC<ConnectorEditViewProps> = ({
	connector,
	periodicEnabled,
	frequencyMinutes,
	enableVisionLlm,
	isSaving,
	isDisconnecting,
	isIndexing = false,
	configPending = false,
	workspaceId,
	onPeriodicEnabledChange,
	onFrequencyChange,
	onEnableVisionLlmChange,
	onSave,
	onDisconnect,
	onBack,
	onQuickIndex,
	onConfigChange,
	onNameChange,
}) => {
	const workspaceIdAtom = useAtomValue(activeWorkspaceIdAtom);
	const isAuthExpired = connector.config?.auth_expired === true;
	const reauthEndpoint = getReauthEndpoint(connector);
	const [reauthing, setReauthing] = useState(false);
	const isMCPBacked = Boolean(connector.config?.server_config);
	const isLive = isMCPBacked;
	const isMCP = connector.connector_type === EnumConnectorName.MCP_CONNECTOR;
	const [togglingEnabled, setTogglingEnabled] = useState(false);
	// `connector.enabled_in_workspace` comes from the REST list
	// (workspaceConnectorList keeps it on every row "Your connectors" opens),
	// but this view keeps the snapshot it was opened with, and a deep link
	// may hand it a row without the field. Ask the status endpoint for the
	// stored state rather than trusting the prop.
	const [localEnabled, setLocalEnabled] = useState(connector.enabled_in_workspace !== false);
	useEffect(() => {
		if (isMCP) return;
		const spaceId = workspaceId ?? workspaceIdAtom;
		if (!spaceId) return;
		let cancelled = false;
		authenticatedFetch(
			buildBackendUrl(`/api/v1/search-source-connectors/${connector.id}/workspaces/${spaceId}`)
		)
			.then((res) => (res.ok ? res.json() : null))
			.then((data) => {
				if (!cancelled && data && typeof data.enabled === "boolean") {
					setLocalEnabled(data.enabled);
				}
			})
			.catch(() => {});
		return () => {
			cancelled = true;
		};
	}, [connector.id, workspaceId, workspaceIdAtom, isMCP]);
	const { mutateAsync: setConnectorEnabled } = useAtomValue(setConnectorEnabledMutationAtom);
	const handleToggleEnabled = useCallback(
		async (enabled: boolean) => {
			const spaceId = workspaceId ?? workspaceIdAtom;
			if (!spaceId) return;
			setTogglingEnabled(true);
			const previous = localEnabled;
			setLocalEnabled(enabled);
			try {
				await setConnectorEnabled({
					connectorId: connector.id,
					workspaceId: Number(spaceId),
					enabled,
				});
			} catch {
				setLocalEnabled(previous);
				toast.error(`Failed to ${enabled ? "enable" : "disable"} ${connector.name}.`);
			} finally {
				setTogglingEnabled(false);
			}
		},
		[workspaceId, workspaceIdAtom, connector.id, connector.name, setConnectorEnabled, localEnabled]
	);
	const supportsVisionLlm = VISION_LLM_CONNECTOR_TYPES.has(connector.connector_type);
	// Another member's account-wide connection opens read-only: only its
	// owner may change or disconnect it (editViewActions).
	const isOthers = !isMCP && connector.is_mine === false;
	const showsVisionToggle = !isLive && !isOthers && supportsVisionLlm && connector.is_indexable;

	const handleReauth = useCallback(async () => {
		const spaceId = workspaceId ?? workspaceIdAtom;
		if (!spaceId || !reauthEndpoint) return;
		setReauthing(true);
		try {
			const response = await authenticatedFetch(
				buildBackendUrl(reauthEndpoint, {
					connector_id: connector.id,
					space_id: spaceId,
					return_url: window.location.pathname,
				})
			);
			if (!response.ok) {
				const data = await response.json().catch(() => ({}));
				toast.error(data.detail ?? "Failed to initiate re-authentication.");
				return;
			}
			const data = await response.json();
			if (data.auth_url) {
				window.location.href = data.auth_url;
			} else if (data.success) {
				toast.success(data.message ?? "Authentication refreshed successfully.");
				window.location.reload();
			}
		} catch {
			toast.error("Failed to initiate re-authentication.");
		} finally {
			setReauthing(false);
		}
	}, [workspaceId, workspaceIdAtom, reauthEndpoint, connector.id]);

	// Get connector-specific config component (MCP-backed connectors use a generic view)
	const ConnectorConfigComponent = useMemo(() => {
		if (isMCPBacked) return MCPServiceConfig;
		if (isOthers) return null;
		return getConnectorConfigComponent(connector.connector_type);
	}, [connector.connector_type, isMCPBacked, isOthers]);
	const { canSave, canDisconnect, disconnectWarning, subtitle, mailboxesNote } = editViewActions(
		connector,
		{ hasSettingsPanel: ConnectorConfigComponent !== null, hasVisionToggle: showsVisionToggle }
	);
	const [isScrolled, setIsScrolled] = useState(false);
	const [hasMoreContent, setHasMoreContent] = useState(false);
	const [showDisconnectConfirm, setShowDisconnectConfirm] = useState(false);
	const [isQuickIndexing, setIsQuickIndexing] = useState(false);
	const scrollContainerRef = useRef<HTMLDivElement>(null);

	const checkScrollState = useCallback(() => {
		if (!scrollContainerRef.current) return;

		const target = scrollContainerRef.current;
		const scrolled = target.scrollTop > 0;
		const hasMore =
			target.scrollHeight > target.clientHeight &&
			target.scrollTop + target.clientHeight < target.scrollHeight - 10;

		setIsScrolled(scrolled);
		setHasMoreContent(hasMore);
	}, []);

	const handleScroll = useCallback(() => {
		checkScrollState();
	}, [checkScrollState]);

	// Check initial scroll state and on resize
	useEffect(() => {
		checkScrollState();
		const resizeObserver = new ResizeObserver(() => {
			checkScrollState();
		});

		if (scrollContainerRef.current) {
			resizeObserver.observe(scrollContainerRef.current);
		}

		return () => {
			resizeObserver.disconnect();
		};
	}, [checkScrollState]);

	// Reset local quick indexing state when indexing completes or fails
	useEffect(() => {
		if (!isIndexing && isQuickIndexing) {
			// Small delay to ensure smooth transition
			const timer = setTimeout(() => {
				setIsQuickIndexing(false);
			}, 100);
			return () => clearTimeout(timer);
		}
	}, [isIndexing, isQuickIndexing]);

	const handleDisconnectClick = () => {
		setShowDisconnectConfirm(true);
	};

	const handleDisconnectConfirm = () => {
		setShowDisconnectConfirm(false);
		onDisconnect();
	};

	const handleDisconnectCancel = () => {
		setShowDisconnectConfirm(false);
	};

	const handleQuickIndex = useCallback(() => {
		if (onQuickIndex && !isQuickIndexing && !isIndexing) {
			setIsQuickIndexing(true);
			onQuickIndex();
		}
	}, [onQuickIndex, isQuickIndexing, isIndexing]);

	return (
		<div className="flex-1 flex flex-col min-h-0 overflow-hidden">
			{/* Fixed Header */}
			<div
				className={cn(
					"flex-shrink-0 transition-shadow duration-200 relative z-10",
					isScrolled && "shadow-sm"
				)}
			>
				{/* Back button */}
				<Button
					variant="ghost"
					type="button"
					onClick={onBack}
					className="mb-6 h-auto w-fit justify-start gap-2 px-0 py-0 text-xs text-muted-foreground hover:bg-transparent hover:text-accent-foreground sm:text-sm"
				>
					<ArrowLeft data-icon="inline-start" />
					Back to connectors
				</Button>

				{/* Connector header */}
				<div className="flex flex-col sm:flex-row items-start sm:items-center gap-4 mb-6">
					<div className="flex gap-4 flex-1 w-full sm:w-auto">
						<div className="flex h-14 w-14 items-center justify-center rounded-xl bg-primary/10 border border-primary/20 shrink-0">
							{getConnectorIcon(connector.connector_type, "size-7")}
						</div>
						<div className="flex-1 min-w-0">
							<h2 className="text-xl sm:text-2xl font-semibold tracking-tight text-wrap whitespace-normal wrap-break-word">
								{getConnectorDisplayName(connector.name)}
							</h2>
							<p className="text-xs sm:text-base text-muted-foreground mt-1">{subtitle}</p>
						</div>
					</div>
					{/* Enabled-in-this-workspace switch: not MCP, owner only */}
					{!isMCP && connector.is_mine !== false && (
						<EnabledInWorkspaceSwitch
							className="shrink-0"
							checked={localEnabled}
							disabled={togglingEnabled}
							onCheckedChange={handleToggleEnabled}
						/>
					)}
					{/* Quick Index Button - hidden for live connectors and when auth is expired */}
					{connector.is_indexable && !isLive && !isOthers && onQuickIndex && !isAuthExpired && (
						<Button
							variant="secondary"
							size="sm"
							onClick={handleQuickIndex}
							disabled={isQuickIndexing || isIndexing || isSaving || isDisconnecting}
							className="text-xs sm:text-sm bg-slate-400/10 dark:bg-white/10 hover:bg-accent hover:text-accent-foreground border-slate-400/20 dark:border-white/20 w-full sm:w-auto"
						>
							{isQuickIndexing || isIndexing ? (
								<>
									<RefreshCw className="mr-2 h-4 w-4 animate-spin" />
									Syncing
								</>
							) : (
								<>
									<RefreshCw className="mr-2 h-4 w-4" />
									Quick Index
								</>
							)}
						</Button>
					)}
				</div>
			</div>

			{/* Scrollable Content */}
			<div className="flex-1 min-h-0 relative overflow-hidden">
				<div ref={scrollContainerRef} className="h-full overflow-y-auto" onScroll={handleScroll}>
					<div className="space-y-6 pb-6 pt-2">
						{mailboxesNote && <p className="text-sm text-muted-foreground">{mailboxesNote}</p>}

						{/* Connector-specific configuration, once the server's copy is in */}
						{configPending && (
							<div
								className="flex items-center gap-2 text-sm text-muted-foreground"
								data-testid="connector-config-loading"
							>
								<Spinner size="sm" />
								Loading this server's settings…
							</div>
						)}
						{ConnectorConfigComponent && !configPending && (
							<ConnectorConfigComponent
								key={connector.id}
								connector={connector}
								onConfigChange={onConfigChange}
								onNameChange={onNameChange}
								workspaceId={workspaceId}
							/>
						)}

						{/* Vision toggle */}
						{showsVisionToggle && (
							<VisionLLMConfig
								enabled={enableVisionLlm}
								onEnabledChange={onEnableVisionLlmChange}
							/>
						)}

						{connector.is_indexable &&
							!isOthers &&
							(() => {
								const requiresFolderSelection =
									connector.connector_type === "GOOGLE_DRIVE_CONNECTOR";
								const selectedFolders =
									(connector.config?.selected_folders as
										| Array<{ id: string; name: string }>
										| undefined) || [];
								const selectedFiles =
									(connector.config?.selected_files as
										| Array<{ id: string; name: string }>
										| undefined) || [];
								const hasItemsSelected = selectedFolders.length > 0 || selectedFiles.length > 0;
								const isDisabled = requiresFolderSelection && !hasItemsSelected;

								return (
									<PeriodicSyncConfig
										enabled={periodicEnabled}
										frequencyMinutes={frequencyMinutes}
										onEnabledChange={onPeriodicEnabledChange}
										onFrequencyChange={onFrequencyChange}
										disabled={isDisabled}
										disabledMessage={
											isDisabled
												? "Pick at least one file above to enable periodic sync"
												: undefined
										}
									/>
								);
							})()}

						{/* Info box - hidden for live connectors */}
						{connector.is_indexable && !isLive && !isOthers && (
							<Alert>
								<Info />
								<AlertDescription>
									You can continue using {BRAND.name} while we sync your data. Check inbox for
									updates.
								</AlertDescription>
							</Alert>
						)}
					</div>
				</div>
				{/* Top fade shadow - appears when scrolled */}
				{isScrolled && (
					<div className="absolute top-0 left-0 right-0 h-6 bg-gradient-to-b from-popover to-transparent pointer-events-none z-10" />
				)}
				{/* Bottom fade shadow - appears when there's more content */}
				{hasMoreContent && (
					<div className="absolute bottom-0 left-0 right-0 h-3 bg-gradient-to-t from-popover to-transparent pointer-events-none z-10" />
				)}
			</div>

			{/* Fixed Footer - Action buttons */}
			<div className="flex-shrink-0 flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3 sm:gap-0 py-6 sm:py-6 bg-transparent">
				{showDisconnectConfirm ? (
					<div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 flex-1 sm:flex-initial">
						<span className="text-xs sm:text-sm text-muted-foreground">{disconnectWarning}</span>
						<div className="flex items-center gap-2 sm:gap-3">
							<Button
								variant="destructive"
								size="sm"
								onClick={handleDisconnectConfirm}
								disabled={isDisconnecting}
								className="relative text-xs sm:text-sm flex-1 sm:flex-initial h-10 sm:h-auto py-2 sm:py-2"
							>
								<span className={isDisconnecting ? "opacity-0" : ""}>Confirm Disconnect</span>
								{isDisconnecting && <Spinner size="sm" className="absolute" />}
							</Button>
							<Button
								variant="ghost"
								size="sm"
								onClick={handleDisconnectCancel}
								disabled={isDisconnecting}
								className="text-xs sm:text-sm flex-1 sm:flex-initial h-10 sm:h-auto py-2 sm:py-2"
							>
								Cancel
							</Button>
						</div>
					</div>
				) : canDisconnect ? (
					<Button
						variant="destructive"
						onClick={handleDisconnectClick}
						disabled={isSaving || isDisconnecting}
						className="text-xs sm:text-sm flex-1 sm:flex-initial h-12 sm:h-auto py-3 sm:py-2"
					>
						Disconnect
					</Button>
				) : null}
				{isAuthExpired && reauthEndpoint && !isOthers ? (
					<Button
						onClick={handleReauth}
						disabled={reauthing || isDisconnecting}
						className="text-xs sm:text-sm flex-1 sm:flex-initial sm:ml-auto h-12 sm:h-auto py-3 sm:py-2 bg-amber-600 hover:bg-amber-700 text-white"
					>
						<RefreshCw className={cn("size-3.5", reauthing && "animate-spin")} />
						Re-authenticate
					</Button>
				) : canSave ? (
					<Button
						onClick={onSave}
						disabled={isSaving || isDisconnecting || configPending}
						className="relative text-xs sm:text-sm flex-1 sm:flex-initial sm:ml-auto h-12 sm:h-auto py-3 sm:py-2"
					>
						<span className={isSaving ? "opacity-0" : ""}>Save Changes</span>
						{isSaving && <Spinner size="sm" className="absolute" />}
					</Button>
				) : null}
			</div>
		</div>
	);
};
