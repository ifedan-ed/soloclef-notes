import { createBuilder, createSchema, relationships } from "@rocicorp/zero";
import { automationRunTable, automationTable } from "./automations";
import { chatSessionStateTable, newChatMessageTable, newChatThreadTable } from "./chat";
import { deliverableJobTable } from "./deliverable-jobs";
import { documentTable, searchSourceConnectorTable } from "./documents";
import { folderTable } from "./folders";
import { notificationTable } from "./inbox";
import { podcastRunTable } from "./podcast-runs";

const newChatMessageRelationships = relationships(newChatMessageTable, ({ one }) => ({
	thread: one({
		sourceField: ["threadId"],
		destSchema: newChatThreadTable,
		destField: ["id"],
	}),
}));

const chatSessionStateThreadRelationships = relationships(chatSessionStateTable, ({ one }) => ({
	thread: one({
		sourceField: ["threadId"],
		destSchema: newChatThreadTable,
		destField: ["id"],
	}),
}));

// A Studio output's thread (the chat or Studio request that made it). threadId
// is NULL for an output made outside a chat (a notebook's audio overview) or
// whose thread was deleted.
const podcastRunRelationships = relationships(podcastRunTable, ({ one }) => ({
	thread: one({
		sourceField: ["threadId"],
		destSchema: newChatThreadTable,
		destField: ["id"],
	}),
}));

const deliverableJobRelationships = relationships(deliverableJobTable, ({ one }) => ({
	thread: one({
		sourceField: ["threadId"],
		destSchema: newChatThreadTable,
		destField: ["id"],
	}),
}));

const automationRunRelationships = relationships(automationRunTable, ({ one }) => ({
	automation: one({
		sourceField: ["automationId"],
		destSchema: automationTable,
		destField: ["id"],
	}),
}));

export const schema = createSchema({
	tables: [
		notificationTable,
		documentTable,
		folderTable,
		searchSourceConnectorTable,
		newChatThreadTable,
		newChatMessageTable,
		chatSessionStateTable,
		automationTable,
		automationRunTable,
		podcastRunTable,
		deliverableJobTable,
	],
	relationships: [
		newChatMessageRelationships,
		chatSessionStateThreadRelationships,
		podcastRunRelationships,
		deliverableJobRelationships,
		automationRunRelationships,
	],
});

export type Schema = typeof schema;

export const zql = createBuilder(schema);
