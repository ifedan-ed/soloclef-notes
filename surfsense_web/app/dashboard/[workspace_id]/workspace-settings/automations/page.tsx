import { AutomationsContent } from "../../automations/automations-content";

/** Automations are workspace configuration, so they are listed in workspace settings. */
export default async function WorkspaceAutomationsSettingsPage({
	params,
}: {
	params: Promise<{ workspace_id: string }>;
}) {
	const { workspace_id } = await params;
	return (
		<div className="w-full space-y-6">
			<AutomationsContent workspaceId={Number(workspace_id)} />
		</div>
	);
}
