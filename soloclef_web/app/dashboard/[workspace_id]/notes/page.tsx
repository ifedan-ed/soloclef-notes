"use client";

import { useParams } from "next/navigation";
import { NotesPage } from "@/features/notes/ui/notes-page";

export default function WorkspaceNotesPage() {
	const params = useParams();
	return <NotesPage workspaceId={Number(params.workspace_id)} />;
}
