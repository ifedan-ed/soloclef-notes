"use client";

import { useParams } from "next/navigation";
import { NotebookWorkspace } from "@/features/notebooks/ui/workspace/notebook-workspace";

export default function NotebookPage() {
	const params = useParams();
	return <NotebookWorkspace notebookId={Number(params.notebook_id)} />;
}
