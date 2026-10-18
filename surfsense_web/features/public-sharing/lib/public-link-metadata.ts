import type { Metadata } from "next";
import { BRAND } from "@/lib/brand";
import enMessages from "@/messages/en.json";

/** What a link preview needs of a public link: what was shared and its title. */
export interface PublicLinkSummary {
	kind: "chat" | "output";
	title: string;
	/** A Studio output's kind ("mindmap", "quiz", ...). */
	outputKind?: string;
}

const KINDS: Record<string, { label?: string }> = enMessages.notebooks.kinds;

/**
 * The page's title and link preview (Open Graph): the shared item's own title
 * and what it is, so a message with the link shows "Planets flashcards" and
 * "Flashcards · shared read-only from …" rather than the site's own card. Never indexed,
 * and the token in the address is not sent on as a referrer.
 */
export function publicLinkMetadata(summary: PublicLinkSummary | null): Metadata {
	const hidden: Metadata = {
		robots: { index: false, follow: false, nocache: true },
		referrer: "no-referrer",
	};
	if (!summary) return { ...hidden, title: BRAND.name };
	// The Studio's own name for the kind ("Flashcards", "Reports"), as a label.
	const what =
		summary.kind === "chat"
			? "Chat"
			: (summary.outputKind && KINDS[summary.outputKind]?.label) || "Studio";
	const description = `${what} · shared read-only from ${BRAND.name}.`;
	const images = [{ url: BRAND.logo.og, width: 1200, height: 630, alt: BRAND.name }];
	return {
		...hidden,
		title: summary.title,
		description,
		openGraph: {
			title: summary.title,
			description,
			siteName: BRAND.name,
			type: "article",
			images,
		},
		twitter: { card: "summary_large_image", title: summary.title, description, images },
	};
}

/** The summary of a public link's answer (GET /api/v1/public/{token}), or null for anything else. */
export function summarizePublicLink(body: unknown): PublicLinkSummary | null {
	if (!body || typeof body !== "object") return null;
	const data = body as {
		kind?: unknown;
		thread?: { title?: unknown };
		output?: { title?: unknown; kind?: unknown } | null;
	};
	if (data.kind === "output" && data.output && typeof data.output.title === "string") {
		return {
			kind: "output",
			title: data.output.title,
			outputKind: typeof data.output.kind === "string" ? data.output.kind : undefined,
		};
	}
	if (typeof data.thread?.title === "string") return { kind: "chat", title: data.thread.title };
	return null;
}
