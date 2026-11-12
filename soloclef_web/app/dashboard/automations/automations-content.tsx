"use client";
import { AlertCircle } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { useAutomations } from "@/hooks/use-automations";
import { AutomationsEmptyState } from "./components/automations-empty-state";
import { AutomationsHeader } from "./components/automations-header";
import { AutomationsTable } from "./components/automations-table";

/**
 * Client orchestrator for the automations list. Pulls the account's first
 * page (via ``useAutomations`` → ``automationsListAtom``) and decides between
 * empty / error / table. Every automation is the account's own, so every
 * action is offered: there is nobody else's permission to check.
 */
export function AutomationsContent() {
	const { automations, total, loading, error } = useAutomations();

	if (error) {
		return (
			<>
				<AutomationsHeader total={0} loading={false} />
				<Alert variant="destructive">
					<AlertCircle aria-hidden />
					<AlertDescription>Couldn't load automations {error.message}</AlertDescription>
				</Alert>
			</>
		);
	}

	if (!loading && automations.length === 0) {
		return (
			<>
				<AutomationsHeader total={0} loading={false} showCreateCta={false} />
				<AutomationsEmptyState />
			</>
		);
	}

	return (
		<>
			<AutomationsHeader total={total} loading={loading} />
			<AutomationsTable automations={automations} loading={loading} />
		</>
	);
}
