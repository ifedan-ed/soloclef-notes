import { defineQueries } from "@rocicorp/zero";
import { automationRunQueries } from "./automations";
import { chatSessionQueries, messageQueries } from "./chat";
import { deliverableJobQueries } from "./deliverable-jobs";
import { connectorQueries, documentQueries } from "./documents";
import { folderQueries } from "./folders";
import { notificationQueries } from "./inbox";
import { podcastRunQueries } from "./podcast-runs";

export const queries = defineQueries({
	notifications: notificationQueries,
	documents: documentQueries,
	folders: folderQueries,
	connectors: connectorQueries,
	messages: messageQueries,
	chatSession: chatSessionQueries,
	automationRuns: automationRunQueries,
	podcastRuns: podcastRunQueries,
	deliverableJobs: deliverableJobQueries,
});
