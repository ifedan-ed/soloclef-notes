/** The document an HTML artifact fragment is shown in, with its security policy. */

/**
 * The frame's policy. Media is refused unless the viewer resolved signed media
 * links, and then only from the origins those links point at.
 */
export function htmlArtifactContentSecurityPolicy(mediaSources: readonly string[] = []): string {
	return [
		"default-src 'none'",
		"script-src 'unsafe-inline'",
		"style-src 'unsafe-inline' https://fonts.googleapis.com",
		"font-src https://fonts.gstatic.com",
		"img-src data:",
		...(mediaSources.length ? [`media-src ${mediaSources.join(" ")}`] : []),
		"connect-src 'none'",
		"form-action 'none'",
		"base-uri 'none'",
		"object-src 'none'",
		"frame-src 'none'",
		"navigate-to 'none'",
	].join("; ");
}

export function buildHtmlArtifactDocument(
	fragment: string,
	{
		mediaSources = [],
		bridgeScript,
	}: {
		mediaSources?: readonly string[];
		/** A viewer-owned inline script run after the fragment (the report view's contents bridge). */
		bridgeScript?: string;
	} = {}
): string {
	return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<meta http-equiv="Content-Security-Policy" content="${htmlArtifactContentSecurityPolicy(mediaSources)}">
</head>
<body>
${fragment}${bridgeScript ? `\n<script>${bridgeScript}</script>` : ""}
</body>
</html>`;
}
