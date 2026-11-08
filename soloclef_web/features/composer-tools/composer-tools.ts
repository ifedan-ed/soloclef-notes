import { AppWindow } from "lucide-react";
import type { ComponentType } from "react";
import { brandIcon } from "@/features/scraper-tools/platform-icons";
import {
	SCRAPER_TOOL_ICONS,
	SCRAPER_TOOL_IDS,
	type ScraperToolId,
} from "@/features/scraper-tools/scraper-tools";

/**
 * Tools that run on the workspace's own key (its Exa, Tavily or Browserbase
 * MCP connector). The menu offers each one only where that key exists.
 */
export const KEYED_TOOL_IDS = ["exa", "tavily", "browserbase"] as const;
export type KeyedToolId = (typeof KEYED_TOOL_IDS)[number];

/** Everything the composer's Tools menu can pick. Ids match the backend's ToolPreference. */
export type ComposerToolId = ScraperToolId | KeyedToolId;

/** Menu order: the general web tools first, then the platforms. */
export const COMPOSER_TOOL_IDS: readonly ComposerToolId[] = [
	"web",
	"google_search",
	...KEYED_TOOL_IDS,
	...SCRAPER_TOOL_IDS.filter((id) => id !== "web" && id !== "google_search"),
];

export const COMPOSER_TOOL_ICONS: Record<ComposerToolId, ComponentType<{ className?: string }>> = {
	...SCRAPER_TOOL_ICONS,
	exa: brandIcon("/connectors/exa.svg", "Exa"),
	tavily: brandIcon("/connectors/tavily.svg", "Tavily"),
	browserbase: AppWindow,
};

export function isKeyedToolId(value: unknown): value is KeyedToolId {
	return typeof value === "string" && (KEYED_TOOL_IDS as readonly string[]).includes(value);
}

/** The tools to list: every platform, plus the keyed tools this workspace has a key for. */
export function offeredComposerTools(connected: ReadonlySet<KeyedToolId>): ComposerToolId[] {
	return COMPOSER_TOOL_IDS.filter((id) => !isKeyedToolId(id) || connected.has(id));
}
