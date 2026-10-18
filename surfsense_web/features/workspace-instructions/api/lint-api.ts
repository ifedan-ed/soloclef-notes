import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";
import type { LintWarning } from "../lib/instructions";

const lintResponse = z.object({
	warnings: z.array(
		z.object({
			code: z.enum(["citations", "outside_knowledge", "safety", "prompt_injection"]),
			quote: z.string(),
			source: z.enum(["rule", "model"]),
		})
	),
	model_checked: z.boolean(),
});

export interface LintResult {
	warnings: LintWarning[];
	model_checked: boolean;
}

export const instructionsLintApi = {
	lint(workspaceId: number, text: string): Promise<LintResult> {
		return baseApiService.post(
			`/api/v1/workspaces/${workspaceId}/system-instructions/lint`,
			lintResponse,
			{ body: { text, use_model: true } }
		);
	},
};
