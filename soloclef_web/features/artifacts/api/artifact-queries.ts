import { type QueryClient, queryOptions } from "@tanstack/react-query";
import { ArtifactListSchema, ArtifactManifestSchema } from "@/features/artifacts/model/artifact";
import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";

export const artifactManifestQueryKey = (artifactId: number) =>
	["artifact-manifest", artifactId] as const;

const artifactImageBlobQueryPrefix = (artifactId: number) =>
	["artifact-image-blob", artifactId] as const;

export const artifactImageBlobQueryKey = (
	artifactId: number,
	shareToken: string | null,
	fileId?: number
) => [...artifactImageBlobQueryPrefix(artifactId), shareToken, fileId] as const;

export const artifactManifestQueryOptions = (artifactId: number) =>
	queryOptions({
		queryKey: artifactManifestQueryKey(artifactId),
		queryFn: async () => {
			const response = await authenticatedFetch(
				buildBackendUrl(`/api/v1/artifacts/${artifactId}/manifest`),
				{ cache: "no-store" }
			);
			if (!response.ok) throw new Error("Artifact could not be loaded");
			const parsed = ArtifactManifestSchema.safeParse(await response.json());
			if (!parsed.success) throw new Error("Artifact response is invalid");
			return parsed.data;
		},
		staleTime: 5_000,
		refetchOnWindowFocus: "always",
	});

export const artifactListQueryKey = (threadId?: number | null) =>
	threadId == null ? (["artifact-list"] as const) : (["artifact-list", threadId] as const);

export async function fetchArtifacts(threadId?: number | null) {
	const query = threadId == null ? "" : `?thread_id=${encodeURIComponent(threadId)}`;
	const response = await authenticatedFetch(buildBackendUrl(`/api/v1/artifacts${query}`), {
		cache: "no-store",
	});
	if (!response.ok) throw new Error("Artifacts could not be loaded");
	const parsed = ArtifactListSchema.safeParse(await response.json());
	if (!parsed.success) throw new Error("Artifact list response is invalid");
	return parsed.data;
}

export const artifactListQueryOptions = (threadId?: number | null) =>
	queryOptions({
		queryKey: artifactListQueryKey(threadId),
		queryFn: () => fetchArtifacts(threadId),
		staleTime: 30_000,
		refetchOnWindowFocus: "always",
	});

/** Refresh every cache surface that can retain an artifact revision. */
export async function invalidatePublishedArtifact(
	queryClient: QueryClient,
	artifactId: number
): Promise<void> {
	const blobPrefixes = [artifactImageBlobQueryPrefix(artifactId)];
	await Promise.all(blobPrefixes.map((queryKey) => queryClient.cancelQueries({ queryKey })));
	for (const queryKey of blobPrefixes) {
		queryClient.setQueriesData<string | null>({ queryKey }, null);
		queryClient.removeQueries({ queryKey });
	}

	await Promise.all([
		queryClient.resetQueries({
			queryKey: artifactManifestQueryKey(artifactId),
			exact: true,
		}),
		queryClient.invalidateQueries({ queryKey: artifactListQueryKey() }),
	]);
}
