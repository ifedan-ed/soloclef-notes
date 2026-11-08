import { redirect } from "next/navigation";

/** The automations list moved into workspace settings; old links land there. */
export default async function AutomationsPage({
	params,
}: {
	params: Promise<{ workspace_id: string }>;
}) {
	const { workspace_id } = await params;
	redirect(`/dashboard/${workspace_id}/workspace-settings/automations`);
}
