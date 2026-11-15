"use client";

import { type QueryClient, useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import {
	type DeliverableJob,
	deliverableJobsApiService,
} from "@/lib/apis/deliverable-jobs-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

export type DeliverableJobStatus = DeliverableJob["status"];

export interface LiveDeliverableJob {
	id: number;
	kind: string;
	title: string;
	status: DeliverableJobStatus;
	phase: string | null;
	progress: number;
	failureCode: string | null;
	artifactId: number | null;
	threadId: number | null;
}

/**
 * A video job's lifecycle, read over REST (GET /deliverable-jobs/{id}) and
 * read again whenever the change stream reports an `outputs` change, which
 * the server sends on every phase, progress and finish write.
 */
export function useDeliverableJobLive(jobId: number | undefined) {
	const { data, isPending } = useQuery({
		queryKey: cacheKeys.deliverableJobs.detail(jobId ?? 0),
		queryFn: () => deliverableJobsApiService.get(jobId as number),
		enabled: !!jobId,
		// A job that is gone is a state the card shows, not an error to toast.
		meta: { suppressGlobalErrorToast: true },
	});

	const job = useMemo<LiveDeliverableJob | undefined>(
		() => (jobId && data ? toLiveJob(data) : undefined),
		[jobId, data]
	);

	return { job, isLoading: !!jobId && isPending };
}

/** Cancel and retry answer with the job as it now is: the card shows it at once. */
export function storeDeliverableJob(queryClient: QueryClient, job: DeliverableJob): void {
	queryClient.setQueryData(cacheKeys.deliverableJobs.detail(job.id), job);
}

function toLiveJob(job: DeliverableJob): LiveDeliverableJob {
	return {
		id: job.id,
		kind: job.kind,
		title: job.title,
		status: job.status,
		phase: job.phase ?? null,
		progress: job.progress,
		failureCode: job.failure_code ?? null,
		artifactId: job.artifact_id ?? null,
		threadId: job.thread_id ?? null,
	};
}
