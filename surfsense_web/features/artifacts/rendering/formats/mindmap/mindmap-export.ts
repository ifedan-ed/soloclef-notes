/**
 * The mind map as it is expanded now, as a standalone SVG (and from it a
 * PNG): the whole tree, not just the part in view.
 */
import {
	CHEVRON_SIZE,
	chevronCentre,
	connectorPath,
	FONT_SIZE,
	type Layout,
	LINE_HEIGHT,
	PAD_X,
	PAD_Y,
	ROOT_FONT_SIZE,
} from "./mindmap-layout";
import { type MindMapPalette, pillColours } from "./mindmap-theme";

const MARGIN = 32;
// After the page's own font: an SVG drawn as an image can't see the page's
// web fonts, and without a sans-serif fallback it came out in Times.
const FALLBACK_FONTS = 'Roboto, "Helvetica Neue", Arial, sans-serif';

function escapeXml(text: string): string {
	return text
		.replaceAll("&", "&amp;")
		.replaceAll("<", "&lt;")
		.replaceAll(">", "&gt;")
		.replaceAll('"', "&quot;");
}

/**
 * ``fontFaceCss`` embeds the page's font (see ``embeddedFontCss``) so the
 * picture uses the same lettering as the screen. Expanded nodes get no
 * chevron button — the picture isn't interactive — and a collapsed one keeps
 * a small › so it's clear the branch continues.
 */
export function mindMapSvg(
	layout: Layout,
	palette: MindMapPalette,
	fontFamily: string,
	fontFaceCss = ""
): string {
	const { bounds } = layout;
	const width = Math.ceil(bounds.width + MARGIN * 2);
	const height = Math.ceil(bounds.height + MARGIN * 2);
	const dx = MARGIN - bounds.x;
	const dy = MARGIN - bounds.y;
	const parts: string[] = [];

	for (const placed of layout.nodes) {
		if (!placed.parentId) continue;
		const parent = layout.byId.get(placed.parentId);
		if (!parent) continue;
		parts.push(
			`<path d="${connectorPath(parent, placed)}" fill="none" stroke="${palette.connector}" stroke-width="1.5"/>`
		);
	}
	for (const placed of layout.nodes) {
		const colours = pillColours(palette, placed.node.depth);
		const root = placed.node.depth === 0;
		const size = root ? ROOT_FONT_SIZE : FONT_SIZE;
		parts.push(
			`<rect x="${placed.x}" y="${placed.y}" width="${placed.width}" height="${placed.height}" rx="${Math.min(18, placed.height / 2)}" fill="${colours.fill}" stroke="${colours.stroke}"/>`
		);
		placed.lines.forEach((line, index) => {
			const baseline = placed.y + PAD_Y + index * LINE_HEIGHT + LINE_HEIGHT / 2;
			parts.push(
				`<text x="${placed.x + PAD_X}" y="${baseline}" dominant-baseline="central" font-size="${size}" font-weight="${root ? 600 : 500}" fill="${colours.text}">${escapeXml(line)}</text>`
			);
		});
		if (placed.node.children.length > 0 && placed.expanded) {
			// Connectors leave from where the on-screen button sits; with no
			// button in the picture, a stub joins them to the pill.
			const centre = chevronCentre(placed);
			parts.push(
				`<line x1="${placed.x + placed.width}" y1="${centre.y}" x2="${centre.x}" y2="${centre.y}" stroke="${palette.connector}" stroke-width="1.5"/>`
			);
		}
		if (placed.node.children.length > 0 && !placed.expanded) {
			const centre = chevronCentre(placed);
			const arm = 3.5;
			const points = `${centre.x - arm / 2},${centre.y - arm} ${centre.x + arm / 2},${centre.y} ${centre.x - arm / 2},${centre.y + arm}`;
			parts.push(
				`<circle cx="${centre.x}" cy="${centre.y}" r="${CHEVRON_SIZE / 2}" fill="${palette.chevronFill}" stroke="${palette.chevronStroke}"/>`,
				`<polyline points="${points}" fill="none" stroke="${palette.chevronIcon}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>`
			);
		}
	}

	return [
		`<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" font-family="${escapeXml(`${fontFamily}, ${FALLBACK_FONTS}`)}">`,
		fontFaceCss ? `<style>${fontFaceCss}</style>` : "",
		`<rect width="100%" height="100%" fill="${palette.background}"/>`,
		`<g transform="translate(${dx} ${dy})">`,
		...parts,
		"</g></svg>",
	].join("");
}

function firstFamily(fontFamily: string): string {
	return (fontFamily.split(",")[0] ?? "").trim().replace(/^["']|["']$/g, "");
}

/** The map draws text at weights 500 and 600; other faces aren't embedded. */
function usesMapWeight(weight: string): boolean {
	const values = weight
		.split(/\s+/)
		.map((value) => (value === "normal" ? 400 : value === "bold" ? 700 : Number(value)))
		.filter((value) => Number.isFinite(value));
	if (values.length === 0) return true;
	const [low, high = low] = values;
	return (low <= 500 && 500 <= high) || (low <= 600 && 600 <= high);
}

function blobToDataUrl(blob: Blob): Promise<string> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => resolve(String(reader.result));
		reader.onerror = () => reject(reader.error);
		reader.readAsDataURL(blob);
	});
}

/**
 * The page's @font-face rules for ``fontFamily``'s first family, with each
 * font file inlined as a data URL, so an exported SVG renders in that font
 * wherever it's drawn. Empty when the rules can't be read (the caller's
 * fallback fonts then apply).
 */
export async function embeddedFontCss(fontFamily: string): Promise<string> {
	const family = firstFamily(fontFamily);
	if (!family || typeof document === "undefined") return "";
	const rules: CSSFontFaceRule[] = [];
	for (const sheet of Array.from(document.styleSheets)) {
		let cssRules: CSSRuleList;
		try {
			cssRules = sheet.cssRules;
		} catch {
			continue; // another origin's stylesheet
		}
		for (const rule of Array.from(cssRules)) {
			if (
				rule instanceof CSSFontFaceRule &&
				firstFamily(rule.style.getPropertyValue("font-family")) === family &&
				usesMapWeight(rule.style.getPropertyValue("font-weight"))
			) {
				rules.push(rule);
			}
		}
	}
	const inlined = await Promise.all(
		rules.map(async (rule) => {
			let css = rule.cssText;
			// A stylesheet's url()s are relative to the stylesheet, not the page.
			const base = rule.parentStyleSheet?.href ?? document.baseURI;
			for (const match of css.matchAll(/url\(["']?([^"')]+)["']?\)/g)) {
				try {
					const response = await fetch(new URL(match[1], base));
					if (!response.ok) return "";
					css = css.replace(match[0], `url("${await blobToDataUrl(await response.blob())}")`);
				} catch {
					return "";
				}
			}
			return css;
		})
	);
	return inlined.filter(Boolean).join("\n");
}

/** Rasterise the SVG at ``scale`` (2 = crisp on high-density screens). */
export async function svgToPng(svg: string, scale = 2): Promise<Blob> {
	const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml" }));
	try {
		const image = new Image();
		image.decoding = "async";
		await new Promise<void>((resolve, reject) => {
			image.onload = () => resolve();
			image.onerror = () => reject(new Error("could not render the mind map"));
			image.src = url;
		});
		// Very large maps are capped so the canvas stays within browser limits.
		const factor = Math.min(scale, 16_000 / Math.max(image.width, image.height, 1));
		const canvas = document.createElement("canvas");
		canvas.width = Math.max(1, Math.round(image.width * factor));
		canvas.height = Math.max(1, Math.round(image.height * factor));
		const context = canvas.getContext("2d");
		if (!context) throw new Error("no canvas");
		context.drawImage(image, 0, 0, canvas.width, canvas.height);
		return await new Promise<Blob>((resolve, reject) =>
			canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error("no image"))), "image/png")
		);
	} finally {
		URL.revokeObjectURL(url);
	}
}

/** Save a blob through an anchor click (no new window). */
export function saveBlob(blob: Blob, filename: string): void {
	const url = URL.createObjectURL(blob);
	const anchor = document.createElement("a");
	anchor.href = url;
	anchor.download = filename;
	anchor.rel = "noopener";
	document.body.appendChild(anchor);
	anchor.click();
	anchor.remove();
	setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function downloadName(title: string | null | undefined, extension: "png" | "svg"): string {
	const base =
		(title ?? "")
			.replace(/[\\/:*?"<>|]+/g, " ")
			.replace(/\s+/g, " ")
			.trim()
			.slice(0, 80) || "mind-map";
	return `${base}.${extension}`;
}
