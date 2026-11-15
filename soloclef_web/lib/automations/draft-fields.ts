/**
 * The fields an automation draft's card lets a person change, read from and
 * written back into the create request the chat's ``create_automation``
 * tool drafted (GET /api/v1/automation-drafts/{id} answers that request
 * exactly as POST /api/v1/automations takes it).
 *
 * Why a module of its own and not the builder's form (builder-schema.ts):
 * the builder edits any automation it can represent and rebuilds the whole
 * definition from its form, so whatever it does not model is dropped or
 * sends the person to JSON. A draft card must change only what the person
 * touched and keep everything else the tool wrote (the goal, the tags, the
 * execution limits) byte for byte, so it patches a copy of the request
 * instead of rebuilding one.
 *
 * The tool always drafts one ``agent_task`` step and one trigger, so those
 * are the only shapes read here; anything else is "not editable on the
 * card" (null), never guessed at. The phone app keeps a copy of this file
 * (soloclef_mobile, src/features/automations/lib/draftFields.ts).
 */

import type { AutomationCreateRequest } from "@/contracts/types/automation.types";

/** What a task may read, as the builder words it (``retrieval_scope``). */
export type DraftReach = "documents" | "web" | "all";

/** When the automation runs: a schedule in a time zone, or a document arriving in a folder. */
export type DraftTrigger =
	| { kind: "schedule"; cron: string; timezone: string }
	| { kind: "event"; folderId: number };

export interface DraftFields {
	name: string;
	/** "" is no description. */
	description: string;
	/** What the assistant is asked at every run (the step's ``query``). */
	task: string;
	trigger: DraftTrigger;
	reach: DraftReach;
	/** Answer only from this notebook's sources (``notebook_id``); null = no confinement. */
	notebookId: number | null;
	/** Keep each result as a note in this notebook (``save_to_notebook_id``); null = not kept. */
	saveToNotebookId: number | null;
	/** Email the owner each run's result (``execution.email_result``). */
	emailResult: boolean;
	/** The model's proxy name (``definition.models.chat``); null = the account's own chat model. */
	model: string | null;
	/** Tools marked "ask first" run without asking (``auto_approve_all``). */
	unattended: boolean;
}

/** The event the tool drafts an event trigger for (soloclef_automations/definition). */
export const DOCUMENT_ENTERED_FOLDER = "document.entered_folder";

function record(value: unknown): Record<string, unknown> | null {
	return value !== null && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: null;
}

function positiveId(value: unknown): number | null {
	return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : null;
}

/** The one trigger's fields, or null for a trigger the card cannot show as a field. */
function triggerFields(trigger: unknown): DraftTrigger | null {
	const t = record(trigger);
	const params = record(t?.params);
	if (!t || !params) return null;
	if (
		t.type === "schedule" &&
		typeof params.cron === "string" &&
		typeof params.timezone === "string"
	) {
		return { kind: "schedule", cron: params.cron, timezone: params.timezone };
	}
	if (t.type === "event" && params.event_type === DOCUMENT_ENTERED_FOLDER) {
		// Only the filter the tool writes ({folder_id: n}) is a field; a hand-written
		// filter with more in it is not something a folder picker can stand for.
		const filter = record(params.filter);
		const keys = filter ? Object.keys(filter) : [];
		const folderId = positiveId(filter?.folder_id);
		if (folderId !== null && keys.length === 1) return { kind: "event", folderId };
	}
	return null;
}

/**
 * The card's fields for a drafted request, or null when the request is not
 * the one-step, one-trigger shape the tool drafts (the card then shows it
 * read-only and says to change it in the chat).
 */
export function draftFields(request: AutomationCreateRequest): DraftFields | null {
	const plan = request.definition?.plan ?? [];
	const triggers = request.triggers ?? [];
	if (plan.length !== 1 || triggers.length !== 1) return null;
	const step = plan[0];
	if (step.action !== "agent_task" || step.when) return null;
	const params = record(step.params) ?? {};
	const trigger = triggerFields(triggers[0]);
	if (!trigger || typeof params.query !== "string") return null;
	// No key is the server's default, documents. Any other value is not one the
	// card's choice can stand for, so it is not guessed at as documents (which
	// Create would then write back as no key): the draft is shown read-only.
	const scope = params.retrieval_scope ?? "documents";
	if (scope !== "documents" && scope !== "web" && scope !== "all") return null;
	const chat = request.definition.models?.chat;
	return {
		name: request.name,
		description: request.description ?? "",
		task: params.query,
		trigger,
		reach: scope,
		notebookId: positiveId(params.notebook_id),
		saveToNotebookId: positiveId(params.save_to_notebook_id),
		emailResult: request.definition.execution?.email_result === true,
		model: typeof chat === "string" && chat.trim() ? chat : null,
		unattended: params.auto_approve_all === true,
	};
}

/**
 * A copy of ``request`` with ``fields`` written into it. Every key the card
 * does not own is kept as drafted; a field set to its "none" value removes
 * its key rather than writing a null the server would refuse.
 */
export function requestWithFields(
	request: AutomationCreateRequest,
	fields: DraftFields
): AutomationCreateRequest {
	const name = fields.name.trim();
	const description = fields.description.trim();
	const step = request.definition.plan[0];
	const params: Record<string, unknown> = { ...(record(step.params) ?? {}) };
	params.query = fields.task.trim();
	params.auto_approve_all = fields.unattended;
	// "documents" is the server's default and is written as no key, as the builder does.
	if (fields.reach === "documents") delete params.retrieval_scope;
	else params.retrieval_scope = fields.reach;
	if (fields.notebookId === null) delete params.notebook_id;
	else params.notebook_id = fields.notebookId;
	if (fields.saveToNotebookId === null) delete params.save_to_notebook_id;
	else params.save_to_notebook_id = fields.saveToNotebookId;

	const { models: _drafted, ...definition } = request.definition;
	const trigger = request.triggers[0];
	return {
		...request,
		name,
		description: description || null,
		definition: {
			...definition,
			// The definition's own name follows the automation's, as the builder keeps them.
			name,
			plan: [{ ...step, params }, ...request.definition.plan.slice(1)],
			execution: { ...request.definition.execution, email_result: fields.emailResult },
			// No model chosen writes no ``models``: runs are then answered by the
			// account's chat model as it is when each run starts.
			...(fields.model !== null ? { models: { chat: fields.model } } : {}),
		},
		triggers: [
			fields.trigger.kind === "schedule"
				? {
						...trigger,
						type: "schedule",
						params: { cron: fields.trigger.cron.trim(), timezone: fields.trigger.timezone },
					}
				: {
						...trigger,
						type: "event",
						params: {
							event_type: DOCUMENT_ENTERED_FOLDER,
							filter: { folder_id: fields.trigger.folderId },
						},
					},
		],
	};
}

/**
 * What stops the card from sending the request, in words. Only what the
 * card itself can see: the server checks everything again when Create is
 * pressed (the notebooks are the person's, the model is offered, the
 * schedule ever fires) and its answer is shown as it comes.
 */
export function draftFieldProblems(fields: DraftFields): string[] {
	const problems: string[] = [];
	if (!fields.name.trim()) problems.push("Give the automation a name.");
	else if (fields.name.trim().length > 200)
		problems.push("The name is longer than 200 characters.");
	if (!fields.task.trim()) problems.push("Say what the automation should do.");
	if (fields.trigger.kind === "schedule") {
		if (fields.trigger.cron.trim().split(/\s+/).length !== 5)
			problems.push("The schedule needs five fields.");
		if (!fields.trigger.timezone.trim()) problems.push("Choose a time zone.");
	}
	// The server's rule (definition.ParseTask): a step confined to a notebook
	// reads that notebook's sources and nothing else.
	if (fields.notebookId !== null && fields.reach !== "documents") {
		problems.push("A task that answers only from one notebook cannot also search the web.");
	}
	return problems;
}
