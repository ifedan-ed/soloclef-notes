import { z } from "zod";
import { baseApiService } from "./base-api.service";

export const deliverableJobResponse = z.object({
	id: z.number(),
	kind: z.string(),
	title: z.string(),
	status: z.enum(["queued", "running", "cancelling", "cancelled", "failed", "ready"]),
	phase: z.string().nullish(),
	progress: z.number().min(0).max(100),
	failure_code: z.string().nullish(),
	artifact_id: z.number().nullish(),
	thread_id: z.number().nullish(),
	created_at: z.string(),
	updated_at: z.string(),
});

export const deliverableJobActionPath = (jobId: number, action: "cancel" | "retry") =>
	`/api/v1/deliverable-jobs/${jobId}/${action}`;

export const deliverableJobActionRequestOptions = (isDesktopClient: boolean) => ({
	sameOrigin: !isDesktopClient,
});

class DeliverableJobsApiService {
	private postAction(jobId: number, action: "cancel" | "retry") {
		return baseApiService.post(
			deliverableJobActionPath(jobId, action),
			deliverableJobResponse,
			deliverableJobActionRequestOptions(baseApiService.isDesktopClient)
		);
	}

	cancel = (jobId: number) => this.postAction(jobId, "cancel");

	retry = (jobId: number) => this.postAction(jobId, "retry");
}

export const deliverableJobsApiService = new DeliverableJobsApiService();
