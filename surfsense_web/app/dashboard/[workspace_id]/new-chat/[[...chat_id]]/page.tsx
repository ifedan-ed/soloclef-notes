import { redirect } from "next/navigation";
import { workspaceHomePath } from "@/lib/workspace-home";

/** The standalone chat is retired: its old links land on the notebooks home. */
export default async function NewChatPage({
	params,
}: {
	params: Promise<{ workspace_id: string }>;
}) {
	const { workspace_id } = await params;
	redirect(workspaceHomePath(workspace_id));
}
