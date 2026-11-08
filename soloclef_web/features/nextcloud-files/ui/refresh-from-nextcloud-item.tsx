"use client";

import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { useRefreshFromNextcloud } from "../hooks/use-refresh-from-nextcloud";
import { RefreshFromNextcloudLabel } from "./refresh-from-nextcloud-label";

interface RefreshFromNextcloudItemProps {
	connectorId: number;
	documentId: number;
	className?: string;
}

/**
 * "Refresh from Nextcloud" in a document's ⋮ menu. The menu stays open and
 * the item itself reports the result, so nothing pops up over the page.
 */
export function RefreshFromNextcloudItem({
	connectorId,
	documentId,
	className,
}: RefreshFromNextcloudItemProps) {
	const { state, refresh } = useRefreshFromNextcloud(connectorId, documentId);
	return (
		<DropdownMenuItem
			className={className}
			onSelect={(event) => {
				event.preventDefault();
				void refresh();
			}}
		>
			<RefreshFromNextcloudLabel state={state} />
		</DropdownMenuItem>
	);
}
