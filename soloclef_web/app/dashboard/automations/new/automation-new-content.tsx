"use client";
import { AutomationBuilderForm } from "../components/builder/automation-builder-form";
import { AutomationNewHeader } from "./components/automation-new-header";

/**
 * Orchestrator for the create route. The builder defaults to the friendly
 * form with a raw-JSON escape hatch.
 *
 * Model eligibility is not gated here: the builder's own model pickers list
 * eligible models, surface a per-slot notice when none exist, and block
 * submit until each slot resolves.
 */
export function AutomationNewContent() {
	return (
		<AutomationBuilderForm
			mode="create"
			renderModeSwitcher={(modeSwitcher) => <AutomationNewHeader modeSwitcher={modeSwitcher} />}
		/>
	);
}
