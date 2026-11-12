export function artifactDownloadPath(artifactId: number): string {
	return `/api/v1/artifacts/${artifactId}/download`;
}
