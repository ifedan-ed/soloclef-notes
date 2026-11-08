import assert from "node:assert/strict";
import test from "node:test";

import {
	buildCreatePayload,
	createEmptyForm,
	emptyTask,
	hydrateForm,
} from "@/lib/automations/builder-schema";

test("save_to_notebook_id is emitted on the step when set", () => {
	const form = createEmptyForm();
	form.name = "Digest";
	form.tasks = [{ ...emptyTask(), query: "Summarize new docs", saveToNotebookId: 7 }];
	const payload = buildCreatePayload(form, 1);
	const step = payload.definition.plan[0] as { params: Record<string, unknown> };
	assert.equal(step.params.save_to_notebook_id, 7);
});

test("save_to_notebook_id is omitted when unset (don't save)", () => {
	const form = createEmptyForm();
	form.name = "Digest";
	form.tasks = [{ ...emptyTask(), query: "Summarize new docs" }];
	const payload = buildCreatePayload(form, 1);
	const step = payload.definition.plan[0] as { params: Record<string, unknown> };
	assert.equal("save_to_notebook_id" in step.params, false);
});

test("email_result is carried onto execution when the owner asks to be emailed", () => {
	const form = createEmptyForm();
	form.name = "Digest";
	form.tasks = [{ ...emptyTask(), query: "Summarize new docs" }];
	form.execution.emailResult = true;
	const payload = buildCreatePayload(form, 1);
	assert.equal(
		(payload.definition.execution as { email_result: boolean }).email_result,
		true
	);
});

test("hydrateForm round-trips save_to_notebook_id and execution.email_result", () => {
	const result = hydrateForm(
		"Digest",
		null,
		{
			schema_version: "1.0",
			name: "Digest",
			plan: [
				{
					step_id: "step_1",
					action: "agent_task",
					params: { query: "Summarize", save_to_notebook_id: 9 },
				},
			],
			execution: { email_result: true },
			metadata: {},
		},
		[]
	);
	assert.equal(result.formable, true);
	if (!result.formable) return;
	assert.equal(result.form.tasks[0]?.saveToNotebookId, 9);
	assert.equal(result.form.execution.emailResult, true);
});

test("hydrateForm defaults email_result to false and saveToNotebookId to null when absent", () => {
	const result = hydrateForm(
		"Digest",
		null,
		{
			schema_version: "1.0",
			name: "Digest",
			plan: [
				{
					step_id: "step_1",
					action: "agent_task",
					params: { query: "Summarize" },
				},
			],
			metadata: {},
		},
		[]
	);
	assert.equal(result.formable, true);
	if (!result.formable) return;
	assert.equal(result.form.tasks[0]?.saveToNotebookId, null);
	assert.equal(result.form.execution.emailResult, false);
});
