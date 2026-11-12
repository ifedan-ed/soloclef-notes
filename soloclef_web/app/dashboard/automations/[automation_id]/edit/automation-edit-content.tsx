"use client";
import { useAutomation } from "@/hooks/use-automation";
import { AutomationBuilderForm } from "../../components/builder/automation-builder-form";
import { AutomationDetailLoading } from "../components/automation-detail-loading";
import { AutomationNotFound } from "../components/automation-not-found";
import { AutomationEditHeader } from "./components/automation-edit-header";

interface AutomationEditContentProps {
	automationId: number;
}

/** Client orchestrator for the edit route; mirrors detail-content's branches. */
export function AutomationEditContent({ automationId }: AutomationEditContentProps) {
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
		<AutomationBuilderForm
			mode="edit"
			automation={automation}
			renderModeSwitcher={(modeSwitcher) => (
				<AutomationEditHeader automation={automation} modeSwitcher={modeSwitcher} />
			)}
		/>
	);
}
