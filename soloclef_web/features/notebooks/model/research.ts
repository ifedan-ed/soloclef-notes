import { z } from "zod";
import { RESEARCH_SOURCE_IDS } from "./research-sources";

export const researchModes = ["fast", "deep"] as const;
export type ResearchMode = (typeof researchModes)[number];

export const researchPhases = ["searching", "reading", "ranking"] as const;
export type ResearchPhase = (typeof researchPhases)[number];

export const researchSourceSchema = z.enum(RESEARCH_SOURCE_IDS);

export const paperBadges = ["pubmed", "pmc", "arxiv", "medrxiv", "biorxiv", "scholar"] as const;
export type PaperBadge = (typeof paperBadges)[number];

/** What a paper result knows about itself: ids for finding it again, byline, access. */
export const paperSchema = z.object({
	doi: z.string().nullish(),
	pmid: z.string().nullish(),
	pmcid: z.string().nullish(),
	arxiv_id: z.string().nullish(),
	server: z.enum(["medrxiv", "biorxiv"]).nullish(),
	version: z.string().nullish(),
	pdf_url: z.string().nullish(),
	authors: z.array(z.string()).default([]),
	venue: z.string().nullish(),
	year: z.number().nullish(),
	published: z.string().nullish(),
	categories: z.array(z.string()).default([]),
	cited_by: z.number().nullish(),
	// Kept loose: a badge the web does not know yet is simply not drawn.
	badges: z.array(z.string()).default([]),
	open_access: z.boolean().default(false),
});
export type Paper = z.infer<typeof paperSchema>;

export interface ResearchRequest {
	query: string;
	/** Where the search mainly looks: the first of ``sources`` when there are several. */
	source: z.infer<typeof researchSourceSchema>;
	/** Several sources searched side by side (at most ``MAX_RESEARCH_SOURCES``). */
	sources?: z.infer<typeof researchSourceSchema>[];
	mode: ResearchMode;
}

/** How one source of a several-source search is getting on. */
export const researchProviderStatuses = ["searching", "done", "error"] as const;
export type ResearchProviderStatus = (typeof researchProviderStatuses)[number];

export const researchResultSchema = z.object({
	id: z.string(),
	url: z.string(),
	title: z.string(),
	snippet: z.string(),
	// Kept loose: a source the web does not know yet still renders, with a generic icon.
	source: z.string(),
	// Links to our own image proxy, never the remote image itself.
	thumbnail_url: z.string().nullish(),
	favicon_url: z.string().nullish(),
	meta: z.string().nullish(),
	paper: paperSchema.nullish(),
});
export type ResearchResult = z.infer<typeof researchResultSchema>;

export const researchEventSchema = z.discriminatedUnion("type", [
	z.object({
		type: z.literal("phase"),
		phase: z.enum(researchPhases),
		progress: z.number().min(0).max(100),
	}),
	z.object({ type: z.literal("results"), results: z.array(researchResultSchema) }),
	z.object({
		type: z.literal("error"),
		message: z.string(),
		// Lets the panel word the failure in the user's language.
		code: z.string().nullish(),
		// The source the failure is worded after, when several were searched.
		source: z.string().nullish(),
	}),
	/** One source of a several-source search started, answered, or failed. */
	z.object({
		type: z.literal("provider"),
		source: z.string(),
		status: z.enum(researchProviderStatuses),
		count: z.number().int().min(0).nullish(),
		code: z.string().nullish(),
		message: z.string().nullish(),
	}),
	z.object({ type: z.literal("done") }),
]);
export type ResearchEvent = z.infer<typeof researchEventSchema>;

export interface ResearchImportItem {
	url: string;
	title: string;
	snippet: string;
	source: string;
	/** Sent back so the import can read the paper's full text and spot duplicates. */
	paper?: Paper;
}

/** The backend accepts at most this many results per import. */
export const MAX_RESEARCH_IMPORT = 50;

/** Sources this workspace cannot search, each with why (e.g. no Exa key connected). */
export const researchSourcesStatusSchema = z.object({
	unavailable: z.record(z.string(), z.literal("not_connected")).default({}),
	/** What "Read a web page" reads with: Browserbase when it is connected. */
	page_reader: z.enum(["browserbase", "crawler"]).default("crawler"),
});
export type ResearchSourcesStatus = z.infer<typeof researchSourcesStatusSchema>;
