import { RolesManager } from "@/components/settings/roles-manager";
import { RolesSummary } from "@/features/workspace-team/ui/roles-summary";

export default async function Page({ params }: { params: Promise<{ workspace_id: string }> }) {
	const { workspace_id } = await params;
	return (
		<div className="space-y-6">
			<RolesSummary />
			<RolesManager workspaceId={Number(workspace_id)} />
		</div>
	);
}
