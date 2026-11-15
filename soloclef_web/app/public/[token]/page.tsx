import type { Metadata } from "next";
import { publicLinkMetadata } from "@/features/public-sharing/lib/public-link-metadata";
import { BRAND, OG_IMAGES } from "@/lib/brand";
import { STATIC_PLACEHOLDER } from "@/lib/routing/dynamic-segments";
import { PublicLinkRoute } from "./public-link-route";

// Never indexed, and the token is not sent on as a referrer. The title and
// link preview of the output shared are filled in per link by Caddy when it
// serves this file (scripts/share-card-template.ts), because a message's
// link preview is read by a bot that runs no script. These are the words
// for a link that names no item; the card's tags are all spelled out so the
// template has each one to fill, and none points at the site's front page
// (an og:url there would make some apps show the front page's card).
export const metadata: Metadata = {
	...publicLinkMetadata(null),
	description: BRAND.description,
	openGraph: {
		title: BRAND.name,
		description: BRAND.description,
		siteName: BRAND.name,
		type: "website",
		images: OG_IMAGES,
	},
	twitter: {
		card: "summary_large_image",
		title: BRAND.name,
		description: BRAND.description,
		images: OG_IMAGES,
	},
};

// One file for every link, served by Caddy at /public/<token>.
export function generateStaticParams() {
	return [{ token: STATIC_PLACEHOLDER }];
}

/** A shared Studio output, the only thing a public link can show. */
export default function PublicLinkPage() {
	return <PublicLinkRoute />;
}
