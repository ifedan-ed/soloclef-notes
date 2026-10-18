/**
 * The Local/Cloud parser choice and file-type checks, ported from the web's
 * surfsense_web/lib/etl/parser-choice.ts so an upload from the phone is
 * refused or accepted exactly as the web's upload dialog would.
 */
import type { DocumentParser, EtlCapabilities } from "../api/schemas";

export interface ParserOption {
	parser: DocumentParser;
	available: boolean;
	unavailableReason: string | null;
	costMultiplier: number | null;
}

export interface ParserChoice {
	options: ParserOption[];
	effectiveParser: DocumentParser | undefined;
}

export function resolveParserChoice(
	capabilities: EtlCapabilities | undefined,
	selected: DocumentParser | undefined
): ParserChoice {
	if (!capabilities) return { options: [], effectiveParser: undefined };
	const options = capabilities.parsers.map((capability) => ({
		parser: capability.parser,
		available: capability.available,
		unavailableReason: capability.available ? null : capability.unavailable_reason,
		costMultiplier:
			capabilities.page_credit_billing_enabled && capability.page_multiplier > 0 ? capability.page_multiplier : null,
	}));
	const runnable = (parser: DocumentParser | undefined) =>
		options.some((option) => option.parser === parser && option.available);
	const effectiveParser = runnable(selected)
		? selected
		: runnable(capabilities.default_parser)
			? capabilities.default_parser
			: options.find((option) => option.available)?.parser;
	return { options, effectiveParser };
}

export function acceptedExtensions(capabilities: EtlCapabilities, parser: DocumentParser | undefined): Set<string> {
	const chosen = capabilities.parsers.find(
		(capability) => capability.parser === (parser ?? capabilities.default_parser)
	);
	const { plaintext, direct_convert, audio } = capabilities.always_handled;
	return new Set(
		[...(chosen?.available ? chosen.document_extensions : []), ...plaintext, ...direct_convert, ...audio].map((ext) =>
			ext.toLowerCase()
		)
	);
}

export function fileExtension(name: string): string {
	const dot = name.lastIndexOf(".");
	return dot > 0 ? name.slice(dot).toLowerCase() : "";
}

/** A pasted web address, or null if it is not one the backend will import. */
export function normalizeLink(raw: string): string | null {
	let text = raw.trim();
	if (!text) return null;
	if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(text)) text = `https://${text}`;
	try {
		const url = new URL(text);
		if (url.protocol !== "http:" && url.protocol !== "https:") return null;
		if (!url.hostname || !url.hostname.includes(".")) return null;
		return url.toString();
	} catch {
		return null;
	}
}

/** Which research source a link imports as: YouTube pages are read as videos. */
export function linkSource(url: string): "youtube" | "web" {
	try {
		const host = new URL(url).hostname.toLowerCase().replace(/^www\.|^m\./, "");
		return host === "youtube.com" || host === "youtu.be" || host.endsWith(".youtube.com") ? "youtube" : "web";
	} catch {
		return "web";
	}
}
