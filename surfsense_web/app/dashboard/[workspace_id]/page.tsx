import { redirect } from "next/navigation";
import { workspaceHomePath } from "@/lib/workspace-home";

export default async function WorkspaceDashboardPage({
	params,
}: {
	params: Promise<{ workspace_id: string }>;
}) {
	const { workspace_id } = await params;
	redirect(workspaceHomePath(workspace_id));
}
