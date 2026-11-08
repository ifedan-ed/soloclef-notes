import Image from "next/image";
import { cn } from "@/lib/utils";

/**
 * Full-color brand marks for the scraper tools, served from
 * `/public/connectors/*.svg` (the connector UI's asset library), each a
 * `ComponentType<{ className?: string }>` so it drops in like a Lucide icon.
 */
export function brandIcon(src: string, alt: string) {
	return function BrandIcon({ className }: { className?: string }) {
		return (
			<Image
				src={src}
				alt={alt}
				width={20}
				height={20}
				className={cn("select-none object-contain pointer-events-none", className)}
				draggable={false}
			/>
		);
	};
}

export const AmazonIcon = brandIcon("/connectors/amazon.svg", "Amazon");
export const WalmartIcon = brandIcon("/connectors/walmart.svg", "Walmart");
export const RedditIcon = brandIcon("/connectors/reddit.svg", "Reddit");
export const YouTubeIcon = brandIcon("/connectors/youtube.svg", "YouTube");
export const InstagramIcon = brandIcon("/connectors/instagram.svg", "Instagram");
export const TikTokIcon = brandIcon("/connectors/tiktok.svg", "TikTok");
export const GoogleMapsIcon = brandIcon("/connectors/google-maps.svg", "Google Maps");
export const GoogleSearchIcon = brandIcon("/connectors/google-search.svg", "Google Search");
export const IndeedIcon = brandIcon("/connectors/indeed.svg", "Indeed");
export const WebIcon = brandIcon("/connectors/web.svg", "Web");
