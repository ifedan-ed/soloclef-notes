"use client";

import { useParams } from "next/navigation";
import { NotebooksHome } from "@/features/notebooks/ui/home/notebooks-home";

export default function NotebooksPage() {
	const params = useParams();
	return <NotebooksHome workspaceId={Number(params.workspace_id)} />;
}
