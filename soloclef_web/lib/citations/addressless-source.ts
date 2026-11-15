// Citations of sources that have no address a reader could open: an email
// in the person's own mailbox, and a result from one of their connected apps
// (MCP servers). The server (soloclef_assistant/cite) writes them as
//
//   [citation:src_mail_<B64>]    [citation:src_app_<B64>]
//
// where <B64> is base64url without padding (RFC 4648 section 5) of a short
// UTF-8 label, "Ann Smith <ann@example.org> · Invoice 4411 · 2026-11-03".
//
// Why the label travels in the marker: a mail message or an app's answer
// has nothing a page could fetch later (no document, no address), and the
// chip must still say what it cites. Carrying the words makes the chip work
// in a reloaded chat, a saved note and the phone alike, with no request.
// Why base64url: the label is somebody else's text (a subject line) and
// may hold "]" or a newline, which would end the marker early.
//
// No React and no DOM here: the parser, the numbering, the note editor and
// the phone app's copy of this file all read markers through it.

/** Where an addressless source came from. */
export type AddresslessSource = "mail" | "app";

/** One addressless citation, as the parser hands it to a chip. */
export interface SourceCitationToken {
	kind: "source";
	source: AddresslessSource;
	/** The decoded words the chip shows. */
	label: string;
	/**
	 * The marker's value exactly as written ("src_mail_…"). It is what the
	 * numbering keys the source by and what the note editor writes back, so
	 * a saved note round-trips byte for byte.
	 */
	ref: string;
}

/** The marker values, as an alternative of CITATION_REGEX. */
export const SOURCE_MARKER_PATTERN = "src_(?:mail|app)_[A-Za-z0-9_-]+";

/**
 * The most bytes a label may decode to: the server's own bound (200, cite.
 * MaxLabelBytes) and the "…" (3 bytes) it adds to a label it cut. A longer
 * one was not written by the server, so it is not drawn rather than drawn cut.
 */
export const MAX_SOURCE_LABEL_BYTES = 200 + 3;

const PREFIXES: readonly [string, AddresslessSource][] = [
	["src_mail_", "mail"],
	["src_app_", "app"],
];

/**
 * The citation a marker value names, or null when the value is not an
 * addressless marker or does not decode to a label (not base64url, not
 * UTF-8, empty or too long). A null marker is not drawn at all: a chip
 * that cannot say what it cites would be a citation the reader cannot
 * check, and the raw letters mean nothing to them.
 */
export function decodeSourceMarker(value: string): SourceCitationToken | null {
	const ref = value.trim();
	for (const [prefix, source] of PREFIXES) {
		if (!ref.startsWith(prefix)) continue;
		const bytes = base64UrlBytes(ref.slice(prefix.length));
		if (bytes === null || bytes.length === 0 || bytes.length > MAX_SOURCE_LABEL_BYTES) return null;
		const label = strictUtf8(bytes);
		return label === null ? null : { kind: "source", source, label, ref };
	}
	return null;
}

/** Whether a marker value is written in the addressless form at all. */
export function isSourceMarker(value: string): boolean {
	return value.trim().startsWith("src_");
}

const ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
const SEXTET = new Map([...ALPHABET].map((letter, index) => [letter, index]));

/**
 * base64url without padding to bytes, or null. Written out rather than
 * through atob and TextDecoder because the phone's JavaScript engine has
 * neither everywhere, and the web and the phone must read a marker alike.
 */
function base64UrlBytes(text: string): Uint8Array | null {
	// A length of 1 more than a multiple of four cannot be unpadded base64.
	if (text.length % 4 === 1) return null;
	const out = new Uint8Array(Math.floor((text.length * 3) / 4));
	let bits = 0;
	let held = 0;
	let at = 0;
	for (const letter of text) {
		const value = SEXTET.get(letter);
		if (value === undefined) return null;
		bits = (bits << 6) | value;
		held += 6;
		if (held >= 8) {
			held -= 8;
			out[at++] = (bits >> held) & 0xff;
		}
	}
	return out;
}

/**
 * UTF-8 to text, refusing anything that is not well formed (an overlong
 * form, a surrogate, a byte out of place) instead of putting U+FFFD in it:
 * a label that does not decode was not written by the server.
 */
function strictUtf8(bytes: Uint8Array): string | null {
	let text = "";
	for (let i = 0; i < bytes.length; ) {
		const first = bytes[i];
		let need: number;
		let point: number;
		let least: number;
		if (first < 0x80) {
			text += String.fromCharCode(first);
			i += 1;
			continue;
		} else if (first >= 0xc2 && first <= 0xdf) {
			need = 1;
			point = first & 0x1f;
			least = 0x80;
		} else if (first >= 0xe0 && first <= 0xef) {
			need = 2;
			point = first & 0x0f;
			least = 0x800;
		} else if (first >= 0xf0 && first <= 0xf4) {
			need = 3;
			point = first & 0x07;
			least = 0x10000;
		} else {
			return null;
		}
		// A sequence cut off by the end of the label.
		if (i + need >= bytes.length) return null;
		for (let k = 1; k <= need; k++) {
			const next = bytes[i + k];
			if ((next & 0xc0) !== 0x80) return null;
			point = (point << 6) | (next & 0x3f);
		}
		if (point < least || point > 0x10ffff || (point >= 0xd800 && point <= 0xdfff)) return null;
		text += String.fromCodePoint(point);
		i += need + 1;
	}
	return text;
}
