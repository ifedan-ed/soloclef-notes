"use client";

import type { ToolCallMessagePartProps } from "@assistant-ui/react";
import { z } from "zod";
import { AutomationDraftCard } from "./automation-draft-card";

/**
 * What the server's card allowlist lets through of a finished
 * create_automation call (soloclef_api/internal/chat/cards.go): the draft's
 * id and nothing else. The card reads the draft, and whether it was already
 * created, from the server when it is drawn.
 */
const payloadSchema = z
	.object({ status: z.literal("drafted"), draft_id: z.string().uuid() })
	.strict();

/** The card's payload for a tool result, or null (a call running, or one that failed). */
export function parseAutomationDraftPayload(result: unknown) {
	const parsed = payloadSchema.safeParse(result);
	return parsed.success ? parsed.data : null;
}

/**
 * The chat's create_automation tool: its card, once the tool drafted. A
 * failed call (a draft the server refused, which the model is told about)
 * draws nothing here and stays a step in the turn's trace.
 */
export const CreateAutomationToolUI = ({ result }: ToolCallMessagePartProps) => {
	const payload = parseAutomationDraftPayload(result);
	return payload ? <AutomationDraftCard draftId={payload.draft_id} /> : null;
};
