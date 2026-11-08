import type { Metadata } from "next";
import { PublicChatView } from "@/components/public-chat/public-chat-view";
import { loadPublicLinkSummary } from "@/features/public-sharing/lib/load-public-link-summary";
import { publicLinkMetadata } from "@/features/public-sharing/lib/public-link-metadata";

// The page's title and a message's link preview name what was shared.
export async function generateMetadata({
	params,
}: {
	params: Promise<{ token: string }>;
}): Promise<Metadata> {
	const { token } = await params;
	return publicLinkMetadata(await loadPublicLinkSummary(token));
}

export default async function PublicChatPage({ params }: { params: Promise<{ token: string }> }) {
	const { token } = await params;

	return <PublicChatView shareToken={token} />;
}
