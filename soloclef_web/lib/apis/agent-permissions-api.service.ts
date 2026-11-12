import { z } from "zod";
import { ValidationError } from "@/lib/error";
import { baseApiService } from "./base-api.service";

const ActionEnum = z.enum(["allow", "deny", "ask"]);
export type AgentPermissionAction = z.infer<typeof ActionEnum>;

const AgentPermissionRuleSchema = z.object({
	id: z.number(),
	// A rule is the account's own; with a thread it applies to that chat only.
	thread_id: z.number().nullable(),
	permission: z.string(),
	pattern: z.string(),
	action: ActionEnum,
	created_at: z.string(),
});

export type AgentPermissionRule = z.infer<typeof AgentPermissionRuleSchema>;

const AgentPermissionRuleListSchema = z.array(AgentPermissionRuleSchema);

const AgentPermissionRuleCreateSchema = z.object({
	permission: z
		.string()
		.min(1, "Permission is required")
		.max(255)
		.regex(/^[a-zA-Z0-9_:.\-*]+$/, "Use letters, digits, '.', '_', ':', '-', or '*' wildcards."),
	pattern: z.string().min(1).max(255).default("*"),
	action: ActionEnum,
	thread_id: z.number().nullable().optional(),
});

export type AgentPermissionRuleCreate = z.infer<typeof AgentPermissionRuleCreateSchema>;

const AgentPermissionRuleUpdateSchema = z.object({
	pattern: z.string().min(1).max(255).optional(),
	action: ActionEnum.optional(),
});

export type AgentPermissionRuleUpdate = z.infer<typeof AgentPermissionRuleUpdateSchema>;

class AgentPermissionsApiService {
	// The rules are the signed-in account's own; the server takes the account
	// from the session, so no route names whose rules they are.
	list = async (): Promise<AgentPermissionRule[]> => {
		return baseApiService.get(`/api/v1/agent/permissions/rules`, AgentPermissionRuleListSchema);
	};

	create = async (payload: AgentPermissionRuleCreate): Promise<AgentPermissionRule> => {
		const parsed = AgentPermissionRuleCreateSchema.safeParse(payload);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((i) => i.message).join(", "));
		}
		return baseApiService.post(`/api/v1/agent/permissions/rules`, AgentPermissionRuleSchema, {
			body: parsed.data,
		});
	};

	update = async (
		ruleId: number,
		payload: AgentPermissionRuleUpdate
	): Promise<AgentPermissionRule> => {
		const parsed = AgentPermissionRuleUpdateSchema.safeParse(payload);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((i) => i.message).join(", "));
		}
		return baseApiService.patch(
			`/api/v1/agent/permissions/rules/${ruleId}`,
			AgentPermissionRuleSchema,
			{ body: parsed.data }
		);
	};

	remove = async (ruleId: number): Promise<void> => {
		await baseApiService.delete(`/api/v1/agent/permissions/rules/${ruleId}`);
	};
}

export const agentPermissionsApiService = new AgentPermissionsApiService();
