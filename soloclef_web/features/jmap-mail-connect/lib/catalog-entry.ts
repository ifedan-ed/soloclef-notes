/**
 * How the Stalwart card is found by the connectors catalog's search. It is
 * not in NATIVE_CONNECTORS: those name a search-source connection type, and a
 * Stalwart mailbox is not one (its accounts have their own table), so there is
 * no type to give it. English, as the catalog's other search entries are.
 */
export const JMAP_MAIL_CATALOG_ENTRY = {
	title: "Stalwart (JMAP)",
	description: "Search and read your Stalwart email; draft replies you send yourself",
} as const;
