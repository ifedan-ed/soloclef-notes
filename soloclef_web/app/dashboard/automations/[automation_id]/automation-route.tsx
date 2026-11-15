"use client";

import { usePathSegment } from "@/hooks/use-path-segment";
import { DYNAMIC_PREFIXES } from "@/lib/routing/dynamic-segments";
import { AutomationDetailContent } from "./automation-detail-content";
import { AutomationEditContent } from "./edit/automation-edit-content";

/**
 * The automation the address names, read from the address because one file
 * serves every automation (lib/routing/dynamic-segments). `edit` picks its
 * editor (/dashboard/automations/<id>/edit) over its page.
 */
export function AutomationRoute({ edit = false }: { edit?: boolean }) {
	const segment = usePathSegment(DYNAMIC_PREFIXES.automation);
	if (segment === null) return null;
	const automationId = Number(segment);
	return (
		<div className="w-full space-y-6">
			{edit ? (
				<AutomationEditContent key={segment} automationId={automationId} />
			) : (
				<AutomationDetailContent key={segment} automationId={automationId} />
			)}
		</div>
	);
}
