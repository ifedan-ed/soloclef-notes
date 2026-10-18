"use client";

import { ArrowLeft, Check, Info } from "lucide-react";
import { type FC, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { BRAND } from "@/lib/brand";
import { getConnectorTypeDisplay } from "@/lib/connectors/utils";
import { cn } from "@/lib/utils";
import { PeriodicSyncConfig } from "../../components/periodic-sync-config";
import { VisionLLMConfig } from "../../components/vision-llm-config";
import type { IndexingConfigState } from "../../constants/connector-constants";
import { getConnectorDisplayName } from "../../tabs/all-connectors-tab";
import { getConnectorConfigComponent } from "../index";

const VISION_LLM_CONNECTOR_TYPES = new Set<string>(["GOOGLE_DRIVE_CONNECTOR"]);

interface IndexingConfigurationViewProps {
	config: IndexingConfigState;
	connector?: SearchSourceConnector;
	periodicEnabled: boolean;
	frequencyMinutes: string;
	enableVisionLlm: boolean;
	isStartingIndexing: boolean;
	isFromOAuth?: boolean;
	onPeriodicEnabledChange: (enabled: boolean) => void;
	onFrequencyChange: (frequency: string) => void;
	onEnableVisionLlmChange: (enabled: boolean) => void;
	onConfigChange?: (config: Record<string, unknown>) => void;
	onStartIndexing: () => void;
	onSkip: () => void;
}

export const IndexingConfigurationView: FC<IndexingConfigurationViewProps> = ({
	config,
	connector,
	periodicEnabled,
	frequencyMinutes,
	enableVisionLlm,
	isStartingIndexing,
	isFromOAuth = false,
	onPeriodicEnabledChange,
	onFrequencyChange,
	onEnableVisionLlmChange,
	onConfigChange,
	onStartIndexing,
	onSkip,
}) => {
	// Get connector-specific config component
	const ConnectorConfigComponent = useMemo(
		() => (connector ? getConnectorConfigComponent(connector.connector_type) : null),
		[connector]
	);
	const showsVisionToggle =
		(connector?.is_indexable ?? false) && VISION_LLM_CONNECTOR_TYPES.has(config.connectorType);
	const [isScrolled, setIsScrolled] = useState(false);
	const [hasMoreContent, setHasMoreContent] = useState(false);
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

	return (
		<div className="flex-1 flex flex-col min-h-0 overflow-hidden">
			{/* Fixed Header */}
			<div
				className={cn(
					"shrink-0 transition-shadow duration-200 relative z-10",
					isScrolled && "shadow-sm"
				)}
			>
				{/* Back button - only show if not from OAuth */}
				{!isFromOAuth && (
					<Button
						variant="ghost"
						type="button"
						onClick={onSkip}
						className="mb-6 h-auto w-fit justify-start gap-2 px-0 py-0 text-xs text-muted-foreground hover:bg-transparent hover:text-accent-foreground sm:text-sm"
					>
						<ArrowLeft data-icon="inline-start" />
						Back to connectors
					</Button>
				)}

				{/* Success header */}
				<div className="flex gap-4 mb-6">
					<div className="flex h-14 w-14 items-center justify-center rounded-xl bg-green-500/10 border border-green-500/20">
						<Check className="size-7 text-green-500" />
					</div>
					<div>
						<div className="flex flex-col">
							<span className="text-xl sm:text-2xl font-semibold tracking-tight text-wrap whitespace-normal wrap-break-word">
								{getConnectorTypeDisplay(connector?.connector_type || "")} Connected !
							</span>
							{connector?.name?.includes(" - ") && (
								<span className="text-xl sm:text-xl font-semibold text-muted-foreground tracking-tight text-wrap whitespace-normal wrap-break-word">
									{getConnectorDisplayName(connector.name)}
								</span>
							)}
						</div>
						<p className="text-xs sm:text-base text-muted-foreground mt-1">
							Configure when to start syncing your data
						</p>
					</div>
				</div>
			</div>

			{/* Scrollable Content */}
			<div className="flex-1 min-h-0 relative overflow-hidden">
				<div ref={scrollContainerRef} className="h-full overflow-y-auto" onScroll={handleScroll}>
					<div className="space-y-6 pb-6 pt-2">
						{/* Connector-specific configuration */}
						{ConnectorConfigComponent && connector && (
							<ConnectorConfigComponent connector={connector} onConfigChange={onConfigChange} />
						)}

						{/* Vision toggle */}
						{showsVisionToggle && (
							<VisionLLMConfig
								enabled={enableVisionLlm}
								onEnabledChange={onEnableVisionLlmChange}
							/>
						)}

						{/* Periodic sync stays indexable-only */}
						{connector?.is_indexable && config.connectorType !== "GOOGLE_DRIVE_CONNECTOR" && (
							<PeriodicSyncConfig
								enabled={periodicEnabled}
								frequencyMinutes={frequencyMinutes}
								onEnabledChange={onPeriodicEnabledChange}
								onFrequencyChange={onFrequencyChange}
							/>
						)}

						{/* Info box */}
						{connector?.is_indexable && (
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
			<div className="flex-shrink-0 flex items-center justify-end py-6 bg-transparent">
				<Button
					onClick={onStartIndexing}
					disabled={isStartingIndexing}
					className="text-xs sm:text-sm relative"
				>
					<span className={isStartingIndexing ? "opacity-0" : ""}>Start Indexing</span>
					{isStartingIndexing && <Spinner size="sm" className="absolute" />}
				</Button>
			</div>
		</div>
	);
};
