import type { GetConnectorsRequest } from "@/contracts/types/connector.types";
import type { GetDocumentsRequest } from "@/contracts/types/document.types";
import type { GetLogsRequest } from "@/contracts/types/log.types";

/**
 * Convert an object to a stable array of [key, value] pairs sorted by key.
 * This ensures cache keys are order-independent (avoiding Object.values order-dependency).
 * Filters out undefined values.
 */
function stableEntries(obj: Record<string, unknown> | null | undefined): unknown[] {
	if (!obj) return [];
	return Object.entries(obj)
		.filter(([, v]) => v !== undefined)
		.sort(([a], [b]) => a.localeCompare(b))
		.flat();
}

export const cacheKeys = {
	// New chat threads (assistant-ui)
	threads: {
		detail: (threadId: number) => ["threads", "detail", threadId] as const,
		messages: (threadId: number) => ["threads", "messages", threadId] as const,
	},
	documents: {
		globalQueryParams: (queries: GetDocumentsRequest["queryParams"]) =>
			["documents", ...stableEntries(queries)] as const,
		withQueryParams: (queries: GetDocumentsRequest["queryParams"]) =>
			["documents-with-queries", ...stableEntries(queries)] as const,
		document: (documentId: string) => ["document", documentId] as const,
	},
	logs: {
		list: () => ["logs", "list"] as const,
		detail: (logId: number | string) => ["logs", "detail", logId] as const,
		// The prefix of every summary query; a reader appends its window in
		// hours, so invalidating this refreshes them all.
		summary: () => ["logs", "summary"] as const,
		withQueryParams: (queries: GetLogsRequest["queryParams"]) =>
			["logs", "with-query-params", ...stableEntries(queries)] as const,
	},
	modelConnections: {
		// "own" keeps this key from being a prefix of the three below, so
		// invalidating the account's connections does not refetch the shared ones.
		all: () => ["model-connections", "own"] as const,
		global: () => ["model-connections", "global"] as const,
		globalConfigStatus: () => ["model-connections", "global-config-status"] as const,
		providers: () => ["model-connections", "providers"] as const,
		roles: () => ["model-roles"] as const,
		setupStatus: () => ["llm-setup-status"] as const,
	},
	// The account's chat model: a proxy model name, not a model-role id.
	chatModel: () => ["chat-model"] as const,
	// The model of one of the account's other jobs ("quick", "studio"), also
	// a proxy model name (features/model-choice).
	modelChoice: (job: string) => ["model-choice", job] as const,
	auth: {
		user: ["auth", "user"] as const,
	},
	user: {
		current: () => ["user", "me"] as const,
		// The account's settings: chat instructions, citations, API access.
		settings: () => ["user", "settings"] as const,
		memory: () => ["user", "memory"] as const,
	},
	agentTools: {
		all: () => ["agent-tools"] as const,
	},
	connectors: {
		// "list" keeps this key from being a prefix of the Google Drive folder
		// keys below, so invalidating the list does not refetch open folders.
		all: () => ["connectors", "list"] as const,
		withQueryParams: (queries: GetConnectorsRequest["queryParams"]) =>
			["connectors", ...stableEntries(queries)] as const,
		byId: (connectorId: string) => ["connector", connectorId] as const,
		index: () => ["connector", "index"] as const,
		googleDrive: {
			folders: (connectorId: number, parentId?: string) =>
				["connectors", "google-drive", connectorId, "folders", parentId] as const,
		},
	},
	publicChat: {
		byToken: (shareToken: string) => ["public-chat", shareToken] as const,
	},
	github: {
		repoStars: (username: string, repo: string) =>
			["github", "repo-stars", username, repo] as const,
	},
	liveNotebookLinks: {
		all: ["live-notebook-links"] as const,
		list: ["live-notebook-links", "list"] as const,
		byNotebook: (notebookId: number) => ["live-notebook-links", "notebook", notebookId] as const,
	},
	publicNotebook: {
		byToken: (shareToken: string) => ["public-notebook", shareToken] as const,
	},
	publicChatSnapshots: {
		all: ["public-chat-snapshots"] as const,
		list: ["public-chat-snapshots", "list"] as const,
	},
	prompts: {
		all: () => ["prompts"] as const,
		public: () => ["prompts", "public"] as const,
	},
	notifications: {
		search: (search: string, tab: string) => ["notifications", "search", search, tab] as const,
		sourceTypes: () => ["notifications", "source-types"] as const,
		batchUnreadCounts: () => ["notifications", "unread-counts-batch"] as const,
	},
	etl: {
		// Deployment-wide: the backend reads its parser settings at boot.
		capabilities: () => ["etl", "capabilities"] as const,
	},
	automations: {
		// list endpoint is keyed by pagination too so distinct pages don't collide
		list: (limit: number, offset: number) => ["automations", "list", limit, offset] as const,
		detail: (automationId: number) => ["automations", "detail", automationId] as const,
		runs: (automationId: number, limit: number, offset: number) =>
			["automations", "runs", automationId, limit, offset] as const,
		run: (automationId: number, runId: number) =>
			["automations", "runs", automationId, runId] as const,
	},
};
