import type { ComponentType } from "react";
import {
	AmazonIcon,
	GoogleMapsIcon,
	GoogleSearchIcon,
	IndeedIcon,
	InstagramIcon,
	RedditIcon,
	TikTokIcon,
	WalmartIcon,
	WebIcon,
	YouTubeIcon,
} from "./platform-icons";

/**
 * The scrapers, offered as tools: the notebook's "Search the web for new
 * sources" box and the composer's Tools menu. Ids match the backend's
 * ToolPreference and research source values.
 */
export const SCRAPER_TOOL_IDS = [
	"web",
	"google_search",
	"youtube",
	"reddit",
	"instagram",
	"tiktok",
	"google_maps",
	"indeed",
	"amazon",
	"walmart",
] as const;

export type ScraperToolId = (typeof SCRAPER_TOOL_IDS)[number];

export const SCRAPER_TOOL_ICONS: Record<ScraperToolId, ComponentType<{ className?: string }>> = {
	web: WebIcon,
	google_search: GoogleSearchIcon,
	youtube: YouTubeIcon,
	reddit: RedditIcon,
	instagram: InstagramIcon,
	tiktok: TikTokIcon,
	google_maps: GoogleMapsIcon,
	indeed: IndeedIcon,
	amazon: AmazonIcon,
	walmart: WalmartIcon,
};

export function isScraperToolId(value: unknown): value is ScraperToolId {
	return typeof value === "string" && (SCRAPER_TOOL_IDS as readonly string[]).includes(value);
}
