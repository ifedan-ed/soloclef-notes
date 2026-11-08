import assert from "node:assert/strict";
import test from "node:test";

import type { Automation } from "@/contracts/types/automation.types";
import {
	buildCreatePayload,
	createEmptyForm,
	emptyTask,
	formFromAutomation,
	hasResolvedModels,
} from "@/lib/automations/builder-schema";

function formWith(task: Partial<ReturnType<typeof emptyTask>>, chatModelId: number | null = 0) {
	const form = createEmptyForm();
	return {
		...form,
		name: "Weekly digest",
		tasks: [{ ...emptyTask(), query: "List my notebooks", ...task }],
		models: { chatModelId, imageConfigId: -2001, visionConfigId: -4 },
	};
}

function planParams(payload: ReturnType<typeof buildCreatePayload>) {
	const plan = (payload.definition as unknown as { plan: { params: Record<string, unknown> }[] })
		.plan;
	return plan[0].params;
}

test("a new task reads documents only and is not tied to a notebook", () => {
	const task = emptyTask();
	assert.equal(task.scope, "documents");
	assert.equal(task.notebookId, null);
	const params = planParams(buildCreatePayload(formWith({}), 1));
	assert.equal("retrieval_scope" in params, false);
	assert.equal("notebook_id" in params, false);
});

test("a task that may use the web sends its scope and notebook", () => {
	const params = planParams(buildCreatePayload(formWith({ scope: "all", notebookId: 12 }), 1));
	assert.equal(params.retrieval_scope, "all");
	assert.equal(params.notebook_id, 12);
});

test("Auto (0) is a chosen chat model and is sent as 0", () => {
	const form = formWith({}, 0);
	assert.equal(hasResolvedModels(form.models), true);
	const payload = buildCreatePayload(form, 1);
	const models = (payload.definition as unknown as { models: Record<string, number> }).models;
	assert.equal(models.chat_model_id, 0);
});

test("an unchosen chat model (null) is not resolved", () => {
	assert.equal(createEmptyForm().models.chatModelId, null);
	assert.equal(hasResolvedModels(formWith({}, null).models), false);
});

test("editing reads the scope, notebook and Auto back into the form", () => {
	const automation = {
		id: 1,
		workspace_id: 1,
		name: "Weekly digest",
		description: null,
		status: "active",
		version: 1,
		created_at: "2026-09-27T00:00:00Z",
		updated_at: "2026-09-27T00:00:00Z",
		triggers: [],
		definition: {
			schema_version: "1.0",
			name: "Weekly digest",
			goal: null,
			triggers: [],
			plan: [
				{
					step_id: "step_1",
					action: "agent_task",
					params: {
						query: "Read the new pages",
						auto_approve_all: true,
						retrieval_scope: "web",
						notebook_id: 7,
					},
				},
			],
			execution: {
				timeout_seconds: 600,
				max_retries: 0,
				retry_backoff: "exponential",
				concurrency: "drop_if_running",
				on_failure: [],
			},
			metadata: { tags: [] },
			models: { chat_model_id: 0, image_gen_model_id: -2001, vision_model_id: -4 },
		},
	} as unknown as Automation;
	const result = formFromAutomation(automation);
	assert.equal(result.formable, true);
	if (!result.formable) return;
	assert.equal(result.form.tasks[0].scope, "web");
	assert.equal(result.form.tasks[0].notebookId, 7);
	assert.equal(result.form.models.chatModelId, 0);
});
