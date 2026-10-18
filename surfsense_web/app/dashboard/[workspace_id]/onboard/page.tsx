import { redirect } from "next/navigation";
import { workspaceHomePath } from "@/lib/workspace-home";

/**
 * There is no model onboarding: every workspace starts on the shared models.
 * Old links and bookmarks to /onboard land on the workspace home.
 */
export default async function OnboardPage({
	params,
}: {
	params: Promise<{ workspace_id: string }>;
}) {
	const { workspace_id } = await params;
	redirect(workspaceHomePath(workspace_id));
}
