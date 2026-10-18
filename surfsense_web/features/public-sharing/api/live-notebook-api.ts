import type { ZodType } from "zod";
import { ArtifactManifestSchema } from "@/features/artifacts/model/artifact";
import { buildBackendUrl } from "@/lib/env-config";
import { liveNotebookApiPath } from "../lib/public-link-url";
import { liveNotebookSchema, liveSourceViewSchema } from "../model/live-notebook";

/** A failed read of a live link, carrying the status the page explains (410, 404). */
export class LiveLinkError extends Error {
	constructor(readonly status: number) {
		super(`Live notebook request failed (${status})`);
	}
}

/**
 * Reads without credentials: the link is the only key, so no cookie or
 * session token is ever sent with it.
 */
async function read<T>(path: string, schema: ZodType<T>, signal?: AbortSignal): Promise<T> {
	const response = await fetch(buildBackendUrl(path), {
		credentials: "omit",
		cache: "no-store",
		referrerPolicy: "no-referrer",
		signal,
	});
	if (!response.ok) throw new LiveLinkError(response.status);
	return schema.parse(await response.json());
}

export const liveNotebookApi = {
	page: (token: string, signal?: AbortSignal) =>
		read(liveNotebookApiPath(token), liveNotebookSchema, signal),
	manifest: (token: string, artifactId: number, signal?: AbortSignal) =>
		read(
			liveNotebookApiPath(token, `/artifacts/${artifactId}/manifest`),
			ArtifactManifestSchema,
			signal
		),
	source: (token: string, sourceId: number, signal?: AbortSignal) =>
		read(liveNotebookApiPath(token, `/sources/${sourceId}`), liveSourceViewSchema, signal),
	downloadUrl: (token: string, artifactId: number) =>
		buildBackendUrl(liveNotebookApiPath(token, `/artifacts/${artifactId}/download`)),
};
