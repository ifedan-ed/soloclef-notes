import type { Metadata } from "next";
import { LiveNotebookPage } from "@/features/public-sharing/ui/live-notebook/live-notebook-page";

// The token is the only key: never indexed, never sent on as a referrer.
export const metadata: Metadata = {
	title: "Shared notebook",
	robots: { index: false, follow: false, nocache: true },
	referrer: "no-referrer",
};

export default async function PublicNotebookPage({
	params,
}: {
	params: Promise<{ token: string }>;
}) {
	const { token } = await params;
	return <LiveNotebookPage token={token} />;
}
