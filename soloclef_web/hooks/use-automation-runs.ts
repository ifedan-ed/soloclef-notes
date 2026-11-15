"use client";
import { useQuery } from "@tanstack/react-query";
import type { Run, RunListResponse } from "@/contracts/types/automation.types";
import { automationsApiService } from "@/lib/apis/automations-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

const DEFAULT_LIMIT = 50;

export interface UseAutomationRunsOptions {
	limit?: number;
}

/**
 * Run history for one automation, newest first (GET /automations/{id}/runs).
 * The change stream (`automations`) reports a run starting, moving a step on
 * or finishing, which reads this again, so statuses tick without polling.
 */
export function useAutomationRuns(
	automationId: number | undefined,
	{ limit = DEFAULT_LIMIT }: UseAutomationRunsOptions = {}
) {
	return useQuery<RunListResponse, Error>({
		queryKey: cacheKeys.automations.runs(automationId ?? 0, limit, 0),
		queryFn: () => automationsApiService.listRuns(automationId as number, { limit, offset: 0 }),
		enabled: !!automationId,
	});
}

/**
 * One run in full: its step results, and once it ends its output, artifacts,
 * inputs and error. Its key shares the runs list's prefix, so the stream's
 * refresh of an automation's runs refreshes an open run's steps too.
 */
export function useAutomationRun(
	automationId: number | undefined,
	runId: number | undefined,
	options: { enabled?: boolean } = {}
) {
	const { enabled = true } = options;
	return useQuery<Run, Error>({
		queryKey: cacheKeys.automations.run(automationId ?? 0, runId ?? 0),
		queryFn: () => automationsApiService.getRun(automationId as number, runId as number),
		enabled: enabled && !!automationId && !!runId,
	});
}
