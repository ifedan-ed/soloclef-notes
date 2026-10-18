/**
 * Regenerates every logo file from the brand config.
 *
 *   node scripts/brand-assets.mjs            (from surfsense_web)
 *
 * Reads `lib/brand/brand.json` (name, tagline, colours) and
 * `lib/brand/mark.json` (the mark's geometry) and writes:
 *
 *   public/brand/mark.svg, mark-mono.svg, wordmark.svg, icon-192/512.png,
 *   maskable-512.png; public/icon-128.svg, logo.png, og-image.png,
 *   og-image-twitter.png; app/icon.png, apple-icon.png, favicon.ico;
 *   ../surfsense_mobile/assets/images/* (icon, adaptive icon layers,
 *   monochrome, favicon, splash).
 *
 * Renaming the product is: edit brand.json, run this, rebuild. The in-app mark
 * (`components/Logo.tsx`) reads the same two files at build time.
 *
 * Needs Playwright's Chromium (a devDependency) to rasterise. The OG image
 * asks Google Fonts for Inter and falls back to the system sans if offline.
 */

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const WEB = path.resolve(HERE, "..");
const MOBILE = path.resolve(WEB, "../surfsense_mobile/assets/images");
const brand = JSON.parse(readFileSync(path.join(WEB, "lib/brand/brand.json"), "utf8"));
const mark = JSON.parse(readFileSync(path.join(WEB, "lib/brand/mark.json"), "utf8"));
const { ink, accent, accentOnDark } = brand.colors;

const require = createRequire(path.join(WEB, "package.json"));
const { chromium } = require("@playwright/test");

function escapeXml(text) {
	return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

/** The glyph (lines and dot) scaled about the tile's centre. */
function glyph({ scale = 1, lineColor = "#ffffff", dotColor = accentOnDark } = {}) {
	const c = mark.viewBox / 2;
	const lines = mark.lines.map(([x1, y, x2]) => `<path d="M${x1} ${y}H${x2}"/>`).join("");
	const [dx, dy, dr] = mark.dot;
	return (
		`<g transform="translate(${c} ${c}) scale(${scale}) translate(${-c} ${-c})">` +
		`<g fill="none" stroke="${lineColor}" stroke-width="${mark.lineWidth}" stroke-linecap="round">${lines}</g>` +
		`<circle cx="${dx}" cy="${dy}" r="${dr}" fill="${dotColor}"/></g>`
	);
}

/** The full mark: ink tile, accent fold, the glyph. */
function markSvg({ size = 64, rounded = true, fold = true, tile = true, scale = 1 } = {}) {
	const v = mark.viewBox;
	const r = rounded ? mark.radius : 0;
	return (
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${v} ${v}" width="${size}" height="${size}" role="img" aria-label="${escapeXml(brand.name)}">` +
		`<title>${escapeXml(brand.name)}</title>` +
		(tile ? `<rect width="${v}" height="${v}" rx="${r}" fill="${ink}"/>` : "") +
		(fold ? `<path d="${mark.fold}" fill="${accent}"/>` : "") +
		glyph({ scale }) +
		"</svg>\n"
	);
}

function monoSvg() {
	const v = mark.viewBox;
	return (
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${v} ${v}" width="64" height="64" role="img" aria-label="${escapeXml(brand.name)}">` +
		`<title>${escapeXml(brand.name)}</title>` +
		glyph({ lineColor: "currentColor", dotColor: "currentColor" }) +
		"</svg>\n"
	);
}

/** "Soloclef Notes" -> ["Soloclef", "Notes"]: the last word takes the accent. */
function splitName(name) {
	const at = name.lastIndexOf(" ");
	return at > 0 ? [name.slice(0, at), name.slice(at + 1)] : [name, ""];
}

function wordmarkSvg() {
	const [lead, tail] = splitName(brand.name);
	const v = mark.viewBox;
	const width = 100 + Math.round(brand.name.length * 21);
	return (
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${width} 80" width="${width}" height="80" role="img" aria-label="${escapeXml(brand.name)}">` +
		`<title>${escapeXml(brand.name)}</title>` +
		`<g transform="translate(8 8)"><rect width="${v}" height="${v}" rx="${mark.radius}" fill="${ink}"/><path d="${mark.fold}" fill="${accent}"/>${glyph()}</g>` +
		`<text x="88" y="53" font-family="Inter, 'Segoe UI', Helvetica, Arial, sans-serif" font-size="36" font-weight="700" letter-spacing="-0.8" fill="#111827">${escapeXml(lead)}` +
		(tail ? `<tspan font-weight="500" fill="${accent}"> ${escapeXml(tail)}</tspan>` : "") +
		"</text></svg>\n"
	);
}

function ogHtml() {
	const [lead, tail] = splitName(brand.name);
	return `<html><head><link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;700&display=block" rel="stylesheet"></head>
<body style="margin:0;font-family:Inter,'Segoe UI',Helvetica,Arial,sans-serif">
<div style="width:1200px;height:630px;box-sizing:border-box;padding:84px 96px;background:#f7f8fa;display:flex;flex-direction:column;justify-content:space-between;position:relative;overflow:hidden">
 <div style="position:absolute;right:-120px;top:-120px;width:520px;height:520px;border-radius:50%;background:radial-gradient(circle,#dfe8fd 0,#f7f8fa 70%)"></div>
 <div style="display:flex;align-items:center;gap:28px;position:relative">${markSvg({ size: 120 })}
  <div style="font-size:64px;font-weight:700;letter-spacing:-1.5px;color:#111827">${escapeXml(lead)}${tail ? ` <span style="font-weight:500;color:${accent}">${escapeXml(tail)}</span>` : ""}</div></div>
 <div style="position:relative">
  <div style="font-size:54px;font-weight:700;line-height:1.1;letter-spacing:-1.2px;color:#111827;max-width:960px">${escapeXml(brand.tagline)}</div>
  <div style="margin-top:22px;font-size:28px;color:#4b5563;max-width:980px;line-height:1.35">Cited answers, reports, mind maps, quizzes, slides, audio and video overviews, from the documents and research you add.</div>
 </div>
 <!-- The bottom line once printed one server's host. Every server ships this
      image and the shared link already shows its address, so it stays empty;
      the space keeps the layout. -->
 <div style="font-size:24px;color:#6b7280;position:relative">&nbsp;</div>
</div></body></html>`;
}

/** A Windows .ico holding PNG images (supported since Vista, and by every browser). */
function icoFromPngs(pngs) {
	const header = Buffer.alloc(6);
	header.writeUInt16LE(0, 0);
	header.writeUInt16LE(1, 2);
	header.writeUInt16LE(pngs.length, 4);
	const entries = [];
	let offset = 6 + 16 * pngs.length;
	for (const { size, data } of pngs) {
		const e = Buffer.alloc(16);
		e.writeUInt8(size >= 256 ? 0 : size, 0);
		e.writeUInt8(size >= 256 ? 0 : size, 1);
		e.writeUInt8(0, 2);
		e.writeUInt8(0, 3);
		e.writeUInt16LE(1, 4);
		e.writeUInt16LE(32, 6);
		e.writeUInt32LE(data.length, 8);
		e.writeUInt32LE(offset, 12);
		offset += data.length;
		entries.push(e);
	}
	return Buffer.concat([header, ...entries, ...pngs.map((p) => p.data)]);
}

function write(file, data) {
	mkdirSync(path.dirname(file), { recursive: true });
	writeFileSync(file, data);
	console.log("wrote", path.relative(path.resolve(WEB, ".."), file));
}

const browser = await chromium.launch();
const page = await browser.newPage();

async function render(svg, size, { background } = {}) {
	await page.setViewportSize({ width: size, height: size });
	const sized = svg.replace(/width="\d+" height="\d+"/, 'width="100%" height="100%"');
	await page.setContent(
		`<html><body style="margin:0;background:${background ?? "transparent"}"><div style="width:${size}px;height:${size}px">${sized}</div></body></html>`
	);
	return page.screenshot({ omitBackground: !background });
}

const full = markSvg();
const square = markSvg({ rounded: false, fold: false, scale: 1.05 });
// Android adaptive icons crop to a circle inside the middle 66%: keep the glyph small.
const foreground = markSvg({ tile: false, fold: false, scale: 0.62 });
const background = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="64" height="64"><rect width="64" height="64" fill="${ink}"/></svg>`;

write(path.join(WEB, "public/brand/mark.svg"), full);
write(path.join(WEB, "public/icon-128.svg"), full);
write(path.join(WEB, "public/brand/mark-mono.svg"), monoSvg());
write(path.join(WEB, "public/brand/wordmark.svg"), wordmarkSvg());
write(path.join(WEB, "public/brand/icon-192.png"), await render(full, 192));
write(path.join(WEB, "public/brand/icon-512.png"), await render(full, 512));
write(path.join(WEB, "public/brand/maskable-512.png"), await render(square, 512));
write(path.join(WEB, "app/icon.png"), await render(full, 180));
write(path.join(WEB, "public/logo.png"), await render(full, 180));
write(path.join(WEB, "app/apple-icon.png"), await render(square, 180));
write(
	path.join(WEB, "app/favicon.ico"),
	icoFromPngs(
		await Promise.all([16, 32, 48].map(async (size) => ({ size, data: await render(full, size) })))
	)
);

write(path.join(MOBILE, "icon.png"), await render(square, 1024));
write(path.join(MOBILE, "android-icon-foreground.png"), await render(foreground, 512));
write(path.join(MOBILE, "android-icon-background.png"), await render(background, 512));
write(
	path.join(MOBILE, "android-icon-monochrome.png"),
	await render(
		markSvg({ tile: false, fold: false, scale: 0.62 }).replace(accentOnDark, "#ffffff"),
		432
	)
);
write(path.join(MOBILE, "favicon.png"), await render(full, 48));
write(path.join(MOBILE, "splash-icon.png"), await render(full, 400));
write(path.join(MOBILE, "splash-icon-dark.png"), await render(full, 400));

await page.setViewportSize({ width: 1200, height: 630 });
await page.setContent(ogHtml(), { waitUntil: "networkidle" }).catch(() => {});
await page.evaluate(() => document.fonts.ready);
const og = await page.screenshot();
write(path.join(WEB, "public/og-image.png"), og);
write(path.join(WEB, "public/og-image-twitter.png"), og);

await browser.close();
