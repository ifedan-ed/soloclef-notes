import { redirect } from "next/navigation";
import { workspaceHomePath } from "@/lib/workspace-home";

/** The chat list is retired: chats live in notebooks, listed on the home. */
export default async function ChatsPage({ params }: { params: Promise<{ workspace_id: string }> }) {
	const { workspace_id } = await params;
	redirect(workspaceHomePath(workspace_id));
}
