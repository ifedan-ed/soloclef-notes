/**
 * Gives the public link page (/public/<token>) its link preview, per link,
 * on a site that is static files.
 *
 * Why: a message app shows a link as a card with the shared item's title,
 * and the bot that draws the card reads the page's <head> and runs no
 * script. A Next.js server used to fill that head in for each link
 * (generateMetadata). The site is now one HTML file for every link
 * (lib/routing/dynamic-segments), so this script makes a copy of that file,
 * `<shell>.card.html`, whose title and card tags are a Caddy template: Caddy
 * asks the API what the link shares as it serves the page, and writes its
 * title in (docker/proxy-go/routes.caddy, "Public links").
 *
 * The original file stays as it is. Caddy serves it whenever the template
 * cannot be filled (the API is down or slow), so a link always opens; its
 * card is then the site's own, as it was when the server could not read the
 * link.
 *
 *   pnpm exec tsx scripts/share-card-template.ts out
 */
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { BRAND } from "../lib/brand";
import enMessages from "../messages/en.json";

/** Caddy's template delimiters for this file (routes.caddy names the same). */
export const OPEN = "[[%";
export const CLOSE = "%]]";

/** The internal path Caddy answers with what a link shares, never an error. */
export const SUMMARY_PATH = "/__share_card/";

/** The Studio's name for each kind of output ("Flashcards"), as the app shows it. */
export function outputKindLabels(
	kinds: Record<string, { label?: string }> = enMessages.notebooks.kinds
): Record<string, string> {
	const labels: Record<string, string> = {};
	for (const [kind, entry] of Object.entries(kinds)) {
		if (entry.label) labels[kind] = entry.label;
	}
	return labels;
}

/** A Go string literal. JSON's escapes are a subset of Go's. */
const goString = (value: string) => JSON.stringify(value);

const action = (body: string) => `${OPEN}- ${body} -${CLOSE}`;

/**
 * The template's opening: work out $title, $description and $type from the
 * API's answer for the token in the address, printing nothing. The words are
 * public-link-metadata.ts's: "Planets" and "Flashcards · shared read-only
 * from Soloclef Notes.", and only a shared Studio output names itself.
 */
export function prelude(brandName: string, siteDescription: string, kinds: Record<string, string>) {
	const kindDict = Object.entries(kinds)
		.map(([kind, label]) => `${goString(kind)} ${goString(label)}`)
		.join(" ");
	return [
		action(`$title := ${goString(brandName)}`),
		action(`$description := ${goString(siteDescription)}`),
		action(`$type := "website"`),
		// routes.caddy's matcher captured the token, in the characters a
		// token is made of, so it is safe in a path.
		action(`$token := placeholder "http.regexp.publicLink.1"`),
		action("if $token"),
		action(`$link := httpInclude (printf "${SUMMARY_PATH}%s" $token) | fromJson`),
		action(`if and (kindIs "map" $link) (eq (toString (get $link "kind")) "output")`),
		action(`$output := get $link "output"`),
		action(`if and (kindIs "map" $output) (kindIs "string" (get $output "title"))`),
		action(`$title = get $output "title"`),
		action(`$what := get (dict ${kindDict}) (toString (get $output "kind")) | default "Studio"`),
		action(`$description = printf "%s · shared read-only from %s." $what ${goString(brandName)}`),
		action(`$type = "article"`),
		action("end"),
		action("end"),
		action("end"),
	].join("");
}

/** A tag's content attribute, matched by its name or property. */
function metaPattern(attribute: "name" | "property", key: string) {
	return new RegExp(`(<meta ${attribute}="${key}" content=")[^"]*(")`);
}

/**
 * The page with its title and card tags made template actions, and the
 * prelude in front. Each tag must be there exactly as Next.js writes it: a
 * missing one means the page's metadata changed, and the build stops here
 * rather than ship a card that silently names nothing.
 */
export function cardTemplate(
	html: string,
	options: { brandName: string; siteDescription: string; kinds: Record<string, string> }
): string {
	if (html.includes(OPEN) || html.includes(CLOSE)) {
		throw new Error(`the page already contains ${OPEN} or ${CLOSE}; pick other delimiters`);
	}
	const title = `${OPEN} $title | html ${CLOSE}`;
	const description = `${OPEN} $description | html ${CLOSE}`;
	const replacements: [RegExp, string][] = [
		[/(<title>)[^<]*(<\/title>)/, title],
		[metaPattern("name", "description"), description],
		[metaPattern("property", "og:title"), title],
		[metaPattern("property", "og:description"), description],
		[metaPattern("property", "og:type"), `${OPEN} $type ${CLOSE}`],
		[metaPattern("name", "twitter:title"), title],
		[metaPattern("name", "twitter:description"), description],
	];
	let out = html;
	for (const [pattern, value] of replacements) {
		if (!pattern.test(out)) throw new Error(`the public link page has no ${pattern.source}`);
		out = out.replace(pattern, (_match, before: string, after: string) => before + value + after);
	}
	return prelude(options.brandName, options.siteDescription, options.kinds) + out;
}

/** The public link page's file in the export: public/_.html. */
export function shellPath(outDir: string): string {
	return path.join(outDir, "public", "_.html");
}

function main() {
	const outDir = process.argv[2];
	if (!outDir) throw new Error("usage: tsx scripts/share-card-template.ts <export dir>");
	const shell = shellPath(outDir);
	if (!existsSync(shell)) throw new Error(`${shell} is missing: did the static export run?`);
	const card = cardTemplate(readFileSync(shell, "utf8"), {
		brandName: BRAND.name,
		siteDescription: BRAND.description,
		kinds: outputKindLabels(),
	});
	const target = shell.replace(/\.html$/, ".card.html");
	writeFileSync(target, card);
	console.log(`wrote ${target}`);
}

// Run as a script, not when a test imports it.
if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(__filename)) main();
