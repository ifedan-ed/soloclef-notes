import assert from "node:assert/strict";
import test from "node:test";

import type { AutomationCreateRequest } from "@/contracts/types/automation.types";
import { createViaChatPath, newestFirst } from "@/lib/automations/create-via-chat";
import {
	DOCUMENT_ENTERED_FOLDER,
	draftFieldProblems,
	draftFields,
	requestWithFields,
} from "@/lib/automations/draft-fields";

/** A request as the chat's create_automation tool writes it (soloclef_assistant/automationdrafts). */
function drafted(): AutomationCreateRequest {
	return {
		name: "Asthma papers",
		description: "Monday digest",
		definition: {
			schema_version: "1.0",
			name: "Asthma papers",
			goal: "Kept as drafted",
			triggers: [],
			plan: [
				{
					step_id: "task",
					action: "agent_task",
					params: {
						query: "Summarise new asthma papers.",
						auto_approve_all: true,
						retrieval_scope: "web",
						save_to_notebook_id: 7,
					},
				},
			],
			execution: {
				timeout_seconds: 900,
				max_retries: 1,
				retry_backoff: "linear",
				concurrency: "queue",
				on_failure: [],
				email_result: true,
			},
			metadata: { tags: ["kept"] },
		},
		triggers: [
			{
				type: "schedule",
				params: { cron: "0 8 * * 1", timezone: "Africa/Lagos" },
				static_inputs: {},
				enabled: true,
			},
		],
	};
}

test("a drafted request reads as the card's fields", () => {
	assert.deepEqual(draftFields(drafted()), {
		name: "Asthma papers",
		description: "Monday digest",
		task: "Summarise new asthma papers.",
		trigger: { kind: "schedule", cron: "0 8 * * 1", timezone: "Africa/Lagos" },
		reach: "web",
		notebookId: null,
		saveToNotebookId: 7,
		emailResult: true,
		model: null,
		unattended: true,
	});
});

test("unchanged fields write back the very request, and edits change only their keys", () => {
	const request = drafted();
	const fields = draftFields(request);
	assert.ok(fields);
	assert.deepEqual(requestWithFields(request, fields), { ...request });

	const edited = requestWithFields(request, {
		...fields,
		name: "  Weekly asthma  ",
		description: " ",
		reach: "documents",
		notebookId: 3,
		saveToNotebookId: null,
		emailResult: false,
		model: "fast-model",
		unattended: false,
		trigger: { kind: "schedule", cron: "30 7 * * 1-5", timezone: "Europe/Paris" },
	});
	assert.equal(edited.name, "Weekly asthma");
	assert.equal(edited.definition.name, "Weekly asthma");
	assert.equal(edited.description, null);
	assert.deepEqual(edited.definition.plan[0].params, {
		query: "Summarise new asthma papers.",
		auto_approve_all: false,
		notebook_id: 3,
	});
	assert.deepEqual(edited.definition.models, { chat: "fast-model" });
	// What the card does not own is kept as drafted.
	assert.equal(edited.definition.goal, "Kept as drafted");
	assert.deepEqual(edited.definition.metadata, { tags: ["kept"] });
	assert.equal(edited.definition.execution.timeout_seconds, 900);
	assert.equal(edited.definition.execution.email_result, false);
	assert.deepEqual(edited.triggers[0].params, { cron: "30 7 * * 1-5", timezone: "Europe/Paris" });
	// The account's own model again writes no models at all.
	assert.equal("models" in requestWithFields(edited, { ...fields, model: null }).definition, false);
});

test("an event trigger reads and writes as its folder", () => {
	const request = drafted();
	request.triggers = [
		{
			type: "event",
			params: { event_type: DOCUMENT_ENTERED_FOLDER, filter: { folder_id: 4 } },
			static_inputs: {},
			enabled: true,
		},
	];
	const fields = draftFields(request);
	assert.deepEqual(fields?.trigger, { kind: "event", folderId: 4 });
	assert.ok(fields);
	assert.deepEqual(requestWithFields(request, fields).triggers[0].params, {
		event_type: DOCUMENT_ENTERED_FOLDER,
		filter: { folder_id: 4 },
	});
	// A filter with more in it is not something a folder stands for.
	request.triggers[0].params = {
		event_type: DOCUMENT_ENTERED_FOLDER,
		filter: { folder_id: 4, document_type: "FILE" },
	};
	assert.equal(draftFields(request), null);
});

test("a request of another shape is not editable on the card", () => {
	const two = drafted();
	two.definition.plan = [...two.definition.plan, { ...two.definition.plan[0], step_id: "again" }];
	assert.equal(draftFields(two), null);
	const none = drafted();
	none.triggers = [];
	assert.equal(draftFields(none), null);
	const conditional = drafted();
	conditional.definition.plan[0].when = "inputs.x";
	assert.equal(draftFields(conditional), null);
});

test("a retrieval scope the card has no choice for is not guessed at", () => {
	const odd = drafted();
	odd.definition.plan[0].params.retrieval_scope = "everything";
	assert.equal(draftFields(odd), null);
	const none = drafted();
	delete none.definition.plan[0].params.retrieval_scope;
	assert.equal(draftFields(none)?.reach, "documents");
});

test("what stops Create is said in words", () => {
	const fields = draftFields(drafted());
	assert.ok(fields);
	assert.deepEqual(draftFieldProblems(fields), []);
	assert.deepEqual(
		draftFieldProblems({
			...fields,
			name: " ",
			task: "",
			notebookId: 2,
			reach: "all",
			trigger: { kind: "schedule", cron: "0 8 * *", timezone: "" },
		}),
		[
			"Give the automation a name.",
			"Say what the automation should do.",
			"The schedule needs five fields.",
			"Choose a time zone.",
			"A task that answers only from one notebook cannot also search the web.",
		]
	);
});

test("Create via chat opens a notebook, newest first, with the request started", () => {
	assert.equal(createViaChatPath(12), "/dashboard/notebooks/12?compose=automation");
	const ordered = newestFirst([
		{ id: 1, updated_at: "2026-11-01T00:00:00Z" },
		{ id: 2, updated_at: "2026-11-12T00:00:00Z" },
		{ id: 3, updated_at: "2026-11-12T00:00:00Z" },
	]);
	assert.deepEqual(
		ordered.map((n) => n.id),
		[3, 2, 1]
	);
});
