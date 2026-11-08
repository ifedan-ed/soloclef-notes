import type { UsageQuery } from "./billing-api";

/** Everything billing lives under one root, so a purchase can refresh it all at once. */
export const billingKeys = {
	all: ["billing"] as const,
	catalog: (locale: string) => ["billing", "catalog", locale] as const,
	me: (locale: string) => ["billing", "me", locale] as const,
	usage: (locale: string, query: UsageQuery) => ["billing", "usage", locale, query] as const,
	payments: () => ["billing", "payments"] as const,
	admin: {
		all: ["billing", "admin"] as const,
		overview: () => ["billing", "admin", "overview"] as const,
		users: (q: string, offset: number) => ["billing", "admin", "users", q, offset] as const,
		ledger: (userId: string) => ["billing", "admin", "ledger", userId] as const,
		models: () => ["billing", "admin", "models"] as const,
		report: (days: number) => ["billing", "admin", "report", days] as const,
	},
};
