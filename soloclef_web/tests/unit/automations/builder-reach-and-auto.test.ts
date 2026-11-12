import assert from "node:assert/strict";
import test from "node:test";

import type { Automation } from "@/contracts/types/automation.types";
import {
	buildCreatePayload,
	createEmptyForm,
	emptyTask,
	formFromAutomation,
	hydrateForm,
} from "@/lib/automations/builder-schema";

function formWith(task: Partial<ReturnType<typeof emptyTask>>, chatModel: string | null = null) {
	const form = createEmptyForm();
	return {
		...form,
		name: "Weekly digest",
		tasks: [{ ...emptyTask(), query: "List my notebooks", ...task }],
		chatModel,
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
	const params = planParams(buildCreatePayload(formWith({})));
	assert.equal("retrieval_scope" in params, false);
	assert.equal("notebook_id" in params, false);
});

test("a task that may use the web sends its scope and notebook", () => {
	const params = planParams(buildCreatePayload(formWith({ scope: "all", notebookId: 12 })));
	assert.equal(params.retrieval_scope, "all");
	assert.equal(params.notebook_id, 12);
});

test("no model chosen sends no models at all, so the run follows the account's chat model", () => {
	assert.equal(createEmptyForm().chatModel, null);
	const payload = buildCreatePayload(formWith({}));
	assert.equal("models" in payload.definition, false);
	assert.equal("model" in planParams(payload), false);
});

test("a chosen model is sent by its proxy name, and nothing else is sent beside it", () => {
	const payload = buildCreatePayload(formWith({}, "claude-sonnet-4-5"));
	assert.deepEqual((payload.definition as unknown as { models: unknown }).models, {
		chat: "claude-sonnet-4-5",
	});
});

test("a task's own model is sent on that step only", () => {
	const form = formWith({ model: "gpt-5-mini" }, "claude-sonnet-4-5");
	form.tasks.push({ ...emptyTask(), query: "Then draft the summary" });
	const payload = buildCreatePayload(form);
	const plan = (payload.definition as unknown as { plan: { params: Record<string, unknown> }[] })
		.plan;
	assert.equal(plan[0].params.model, "gpt-5-mini");
	assert.equal("model" in plan[1].params, false);
});

test("a definition saved with the old numeric model ids hydrates as no model chosen", () => {
	// Those ids named rows nothing reads any more, and the server now refuses
	// the keys: the form drops them, so the next save is one it accepts.
	const result = hydrateForm(
		"Digest",
		null,
		{
			name: "Digest",
			plan: [{ step_id: "step_1", action: "agent_task", params: { query: "Summarize" } }],
			models: { chat_model_id: 0, image_gen_model_id: -2001, vision_model_id: -4 },
		},
		[]
	);
	assert.equal(result.formable, true);
	if (!result.formable) return;
	assert.equal(result.form.chatModel, null);
	const payload = buildCreatePayload({ ...result.form });
	assert.equal("models" in payload.definition, false);
});

test("editing reads the scope, notebook and models back into the form", () => {
	const automation = {
		id: 1,
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
						model: "gpt-5-mini",
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
			models: { chat: "claude-sonnet-4-5" },
		},
	} as unknown as Automation;
	const result = formFromAutomation(automation);
	assert.equal(result.formable, true);
	if (!result.formable) return;
	assert.equal(result.form.tasks[0].scope, "web");
	assert.equal(result.form.tasks[0].notebookId, 7);
	assert.equal(result.form.chatModel, "claude-sonnet-4-5");
	assert.equal(result.form.tasks[0].model, "gpt-5-mini");
});
