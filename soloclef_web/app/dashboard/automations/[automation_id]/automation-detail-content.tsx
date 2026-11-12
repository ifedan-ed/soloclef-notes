"use client";
import { useAutomation } from "@/hooks/use-automation";
import { AutomationDefinitionSection } from "./components/automation-definition-section";
import { AutomationDetailHeader } from "./components/automation-detail-header";
import { AutomationDetailLoading } from "./components/automation-detail-loading";
import { AutomationNotFound } from "./components/automation-not-found";
import { AutomationRunsSection } from "./components/automation-runs-section";
import { AutomationTriggersSection } from "./components/automation-triggers-section";

interface AutomationDetailContentProps {
	automationId: number;
}

/**
 * Client orchestrator for one automation's detail view. Branches:
 *   - bad id (NaN) → not-found panel
 *   - detail fetching → skeleton
 *   - detail error / null → not-found panel (someone else's automation is
 *     the same 404 as one that never existed)
 *   - detail loaded → header + definition + runs + triggers
 */
export function AutomationDetailContent({ automationId }: AutomationDetailContentProps) {
	const validId = Number.isInteger(automationId) && automationId > 0;
	const { data: automation, isLoading, error } = useAutomation(validId ? automationId : undefined);

	if (!validId) {
		return <AutomationNotFound />;
	}

	if (isLoading) {
		return <AutomationDetailLoading />;
	}

	if (error || !automation) {
		return <AutomationNotFound error={error} />;
	}

	return (
		<>
			<AutomationDetailHeader automation={automation} />

			<div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
				<div className="space-y-6 min-w-0 lg:col-span-2">
					<AutomationDefinitionSection definition={automation.definition} />
					<AutomationRunsSection automationId={automation.id} />
				</div>
				<div className="space-y-6 min-w-0">
					<AutomationTriggersSection triggers={automation.triggers} automationId={automation.id} />
				</div>
			</div>
		</>
	);
}
