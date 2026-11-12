import type { ComponentType } from "react";
import { brandIcon } from "@/components/ui/brand-icon";

/**
 * Web searches that run on the account's own key (its Exa or Tavily
 * connector). The menu offers each one only where that key exists. They are
 * separate accounts, so they are separate choices.
 */
export const KEYED_TOOL_IDS = ["exa", "tavily"] as const;
export type KeyedToolId = (typeof KEYED_TOOL_IDS)[number];

/**
 * The scholarly literatures, each its own choice because each is its own
 * tool in the chat: PubMed, arXiv, and the bioRxiv and medRxiv preprint
 * servers. They are three entries and not one "academic" entry so that a
 * person asking for peer-reviewed medicine is never answered from preprints.
 * They need no key, so they are always offered.
 */
export const ACADEMIC_TOOL_IDS = ["pubmed", "arxiv", "preprints"] as const;
export type AcademicToolId = (typeof ACADEMIC_TOOL_IDS)[number];

/**
 * Everything the composer's Tools menu can pick, which is exactly what the
 * chat accepts as `preferred_tool` (soloclef_api/internal/chat, `picks`).
 * The chat refuses any other value by name, so a tool is added here only
 * when it is added there. The platform scrapers that used to be listed
 * (Reddit, YouTube and the rest) were cut from the product.
 */
export type ComposerToolId = KeyedToolId | AcademicToolId;

/** Menu order: the web searches first, then the literatures. */
export const COMPOSER_TOOL_IDS: readonly ComposerToolId[] = [
	...KEYED_TOOL_IDS,
	...ACADEMIC_TOOL_IDS,
];

export const COMPOSER_TOOL_ICONS: Record<ComposerToolId, ComponentType<{ className?: string }>> = {
	exa: brandIcon("/connectors/exa.svg", "Exa"),
	tavily: brandIcon("/connectors/tavily.svg", "Tavily"),
	pubmed: brandIcon("/connectors/pubmed.svg", "PubMed"),
	arxiv: brandIcon("/connectors/arxiv.svg", "arXiv"),
	// bioRxiv and medRxiv are one tool; medRxiv's mark stands for the pair.
	preprints: brandIcon("/connectors/medrxiv.svg", "bioRxiv and medRxiv"),
};

/**
 * Each tool's name, as a key of the `composer_tools` messages. Written out
 * and not built from the id, so the type-checker and the locale parity test
 * can see every key that is used.
 */
export const COMPOSER_TOOL_NAME_KEYS = {
	exa: "tool_exa",
	tavily: "tool_tavily",
	pubmed: "tool_pubmed",
	arxiv: "tool_arxiv",
	preprints: "tool_preprints",
} as const satisfies Record<ComposerToolId, string>;

export function isKeyedToolId(value: unknown): value is KeyedToolId {
	return typeof value === "string" && (KEYED_TOOL_IDS as readonly string[]).includes(value);
}

/** The tools to list: every literature, plus the web searches the account has a key for. */
export function offeredComposerTools(connected: ReadonlySet<KeyedToolId>): ComposerToolId[] {
	return COMPOSER_TOOL_IDS.filter((id) => !isKeyedToolId(id) || connected.has(id));
}
