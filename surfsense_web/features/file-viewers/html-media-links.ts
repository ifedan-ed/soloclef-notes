/**
 * Media placeholders in an HTML artifact, resolved to playable links.
 *
 * An interactive notebook report embeds the notebook's audio and video as
 * `<audio|video data-surfsense-media="artifactId:fileId">` with no `src`. The
 * report runs in an iframe sandboxed to an opaque origin, where a player can
 * send neither the session cookie nor a bearer header, so the viewer asks the
 * backend (as the signed-in user) for short-lived signed links and writes them
 * in before the frame is built. A placeholder without a link keeps no `src`,
 * and the report's own script shows its "unavailable" note instead.
 */

export interface MediaRef {
	artifactId: number;
	fileId: number;
}

const PLACEHOLDER = /<(audio|video)\b([^>]*?)\sdata-surfsense-media="(\d+):(\d+)"([^>]*)>/g;
export const MAX_MEDIA_REFS = 24;

export function mediaKey(ref: MediaRef): string {
	return `${ref.artifactId}:${ref.fileId}`;
}

export function findMediaRefs(fragment: string): MediaRef[] {
	const seen = new Map<string, MediaRef>();
	for (const match of fragment.matchAll(PLACEHOLDER)) {
		const ref = { artifactId: Number(match[3]), fileId: Number(match[4]) };
		if (!seen.has(mediaKey(ref))) seen.set(mediaKey(ref), ref);
	}
	return [...seen.values()].slice(0, MAX_MEDIA_REFS);
}

function escapeAttribute(value: string): string {
	return value.replaceAll("&", "&amp;").replaceAll('"', "&quot;").replaceAll("<", "&lt;");
}

/** Give each placeholder its link; an existing `src` is never overwritten. */
export function applyMediaLinks(fragment: string, links: ReadonlyMap<string, string>): string {
	return fragment.replace(
		PLACEHOLDER,
		(whole, tag: string, before: string, artifactId: string, fileId: string, after: string) => {
			const url = links.get(`${artifactId}:${fileId}`);
			if (!url || /\ssrc=/i.test(`${before} ${after}`)) return whole;
			return `<${tag}${before} data-surfsense-media="${artifactId}:${fileId}" src="${escapeAttribute(url)}"${after}>`;
		}
	);
}

/** The workspace an artifact file belongs to, read from its content URL. */
export function workspaceIdFromContentUrl(contentUrl: string): number | null {
	const match = /\/workspaces\/(\d+)\/artifacts\//.exec(contentUrl);
	return match ? Number(match[1]) : null;
}

/** Origins the frame's CSP must allow media from: exactly those of the links. */
export function mediaOrigins(links: ReadonlyMap<string, string>): string[] {
	const origins = new Set<string>();
	for (const url of links.values()) {
		try {
			origins.add(new URL(url).origin);
		} catch {
			// A relative or malformed URL is never written into the frame's policy.
		}
	}
	return [...origins];
}
