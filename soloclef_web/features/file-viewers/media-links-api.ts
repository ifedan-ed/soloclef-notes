import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";
import { type MediaRef, mediaKey } from "./html-media-links";

interface MediaLinkResponse {
	links: { artifact_id: number; file_id: number; url: string }[];
}

/**
 * Signed, absolute links for the given media files, keyed "artifactId:fileId".
 * Any failure yields no links: the report still opens, its players just say
 * the recording is unavailable.
 */
export async function fetchMediaLinks(
	workspaceId: number,
	refs: readonly MediaRef[],
	signal?: AbortSignal
): Promise<Map<string, string>> {
	const links = new Map<string, string>();
	try {
		const response = await authenticatedFetch(
			buildBackendUrl(`/api/v1/workspaces/${workspaceId}/artifacts/media-links`),
			{
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					files: refs.map((ref) => ({ artifact_id: ref.artifactId, file_id: ref.fileId })),
				}),
				cache: "no-store",
				signal,
				skipAuthRedirect: true,
			}
		);
		if (!response.ok) return links;
		const body = (await response.json()) as MediaLinkResponse;
		for (const link of body.links ?? []) {
			const absolute = new URL(buildBackendUrl(link.url), window.location.href).href;
			links.set(mediaKey({ artifactId: link.artifact_id, fileId: link.file_id }), absolute);
		}
	} catch {
		// Aborted or offline: open the report without playable media.
	}
	return links;
}
