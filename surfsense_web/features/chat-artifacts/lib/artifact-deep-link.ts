export const ARTIFACT_QUERY_PARAM = "artifactId";

export function artifactIdFromSearch(search: string): number | null {
	const raw = new URLSearchParams(search).get(ARTIFACT_QUERY_PARAM);
	if (!raw) return null;

	const artifactId = Number(raw);
	return Number.isSafeInteger(artifactId) && artifactId > 0 ? artifactId : null;
}
