/** The three ways to start a fresh notebook's conversation, NotebookLM-style. */
export const GUIDED_STARTS = ["learn", "create", "progress"] as const;
export type GuidedStart = (typeof GUIDED_STARTS)[number];
