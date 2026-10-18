"use client";

import { TriangleAlert } from "lucide-react";
import type { FC } from "react";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";
import type { ConnectorRow } from "../hooks/use-connector-rows";
import { ENABLED_IN_WORKSPACE_LABEL } from "../workspace-enablement/enabled-in-workspace";

const quiet = "ml-auto shrink-0 text-[11px] tabular-nums text-muted-foreground";

/**
 * The trailing mark on a connector row, most important first: switched off
 * in this workspace, syncing, last sync failed, some accounts off, or how
 * many accounts the row holds. Nothing for a single account that is on.
 * Used by "Your connectors" and by the chat composer's connectors menu
 * (desktop submenu and phone drawer), so all three mark a row alike;
 * `className` adjusts size or spacing to the host list.
 */
export const ConnectorRowStatus: FC<{ row: ConnectorRow; className?: string }> = ({
	row,
	className,
}) => {
	if (row.enabledCount === 0) {
		return (
			<span className={cn(quiet, className)} title={`${ENABLED_IN_WORKSPACE_LABEL}: off`}>
				Off
			</span>
		);
	}
	if (row.health === "syncing") {
		return <Spinner size="xs" className={cn("ml-auto shrink-0", className)} />;
	}
	if (row.health === "failed") {
		return (
			<TriangleAlert
				className={cn("ml-auto h-3.5 w-3.5 shrink-0 text-destructive", className)}
				aria-label={row.errorMessage ?? "Indexing failed"}
			/>
		);
	}
	if (row.enabledCount < row.accountCount) {
		return (
			<span className={cn(quiet, className)} title={ENABLED_IN_WORKSPACE_LABEL}>
				{`${row.enabledCount} of ${row.accountCount} on`}
			</span>
		);
	}
	if (row.accountCount > 1) return <span className={cn(quiet, className)}>{row.accountCount}</span>;
	return null;
};
