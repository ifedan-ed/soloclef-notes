import type { ComponentType } from "react";
import { brandIcon } from "@/features/scraper-tools/platform-icons";
import type { WebApiSourceId } from "../../../model/research-sources";

/** Marks for the key-backed web search APIs. */
export const WEB_API_SOURCE_ICONS: Record<WebApiSourceId, ComponentType<{ className?: string }>> = {
	exa: brandIcon("/connectors/exa.svg", "Exa"),
	tavily: brandIcon("/connectors/tavily.svg", "Tavily"),
};
