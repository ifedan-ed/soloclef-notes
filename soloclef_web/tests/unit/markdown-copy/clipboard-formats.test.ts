import assert from "node:assert/strict";
import test from "node:test";
import { UNNAMED_SOURCE } from "@/features/markdown-copy/cited-source";
import { clipboardFormats, numberingForCopy } from "@/features/markdown-copy/clipboard-formats";
import { numberMarkers } from "@/features/markdown-copy/numbered-markdown";
import { namedSources } from "@/features/markdown-copy/source-namer";
import { writeClipboard } from "@/features/markdown-copy/write-clipboard";

// Run with: pnpm exec tsx --test tests/unit/markdown-copy/clipboard-formats.test.ts

const render = (markdown: string, names: string[] = []) => {
	const texts = [markdown];
	const numbering = numberingForCopy(texts);
	const sources = numbering.order.map((key, i) => ({
		number: i + 1,
		name: names[i] ?? key,
		...(key.startsWith("url:") ? { href: key.slice(4) } : {}),
	}));
	return clipboardFormats(texts, numbering, sources);
};

test("citations become the reader's numbers, with the sources listed after", () => {
	const out = render(
		"Fever is common [citation:812], see a page [citation:https://ex.org/a]. Again [citation:812].",
		["Guideline.pdf, p. 2", "https://ex.org/a"]
	);
	assert.equal(
		out.plain,
		"Fever is common [1], see a page [2]. Again [1].\n\nSources\n[1] Guideline.pdf, p. 2\n[2] https://ex.org/a"
	);
	assert.match(out.html, /<p>Fever is common \[1\], see a page \[2\]\. Again \[1\]\.<\/p>/);
	assert.match(
		out.html,
		/<hr><p>Sources<\/p><p>\[1\] Guideline\.pdf, p\. 2<br>\[2\] <a href="https:\/\/ex\.org\/a">/
	);
	assert.doesNotMatch(out.html + out.plain + out.markdown, /citation:/);
});

test("the html keeps the formatting; the plain text has no Markdown marks", () => {
	const out = render(
		"## Results\n\nThe **canary** is *here* and `x = 1`.\n\n1. first\n2. second\n   - nested\n\n| A | B |\n|---|---|\n| 1 | **2** |\n\n```py\nprint('[citation:3]')\n```"
	);
	assert.match(out.html, /<h2>Results<\/h2>/);
	assert.match(out.html, /<strong>canary<\/strong> is <em>here<\/em>/);
	assert.match(out.html, /<ol>\s*<li>first<\/li>/);
	assert.match(out.html, /<table style="border-collapse:collapse">/);
	assert.match(out.html, /<td style="[^"]*">\s*<strong>2<\/strong>/);
	assert.match(
		out.html,
		/<pre style="[^"]*"><code class="language-py"[^>]*>print\('\[citation:3\]'\)/
	);
	assert.equal(
		out.plain,
		"Results\n\nThe canary is here and x = 1.\n\n1. first\n2. second\n   - nested\n\nA | B\n1 | 2\n\nprint('[citation:3]')"
	);
});

test("maths is pasted as readable text, and dollars stay money", () => {
	const out = render("Here \\(r^2 \\le \\alpha\\) and $$\\frac{a}{b}$$ for $3 and $5.");
	assert.match(out.plain, /r² ≤ α/);
	assert.match(out.plain, /a\/b/);
	assert.match(out.plain, /\$3 and \$5/);
	assert.doesNotMatch(out.html, /katex|\\alpha|\\frac/);
});

test("a link keeps its address; an unsafe one loses it", () => {
	const out = render("[docs](https://ex.org/d) and [bad](javascript:alert(1))");
	assert.match(out.html, /<a href="https:\/\/ex\.org\/d">docs<\/a>/);
	assert.doesNotMatch(out.html, /javascript:/);
	assert.equal(out.plain, "docs (https://ex.org/d) and bad");
});

test("raw HTML from the model is not pasted", () => {
	const out = render("before <img src=x onerror=alert(1)> after");
	assert.doesNotMatch(out.html, /onerror/);
});

test("the Markdown download stays Markdown, with numbered citations and sources", () => {
	const out = render("# T\n\n**Bold** [citation:5] and `[citation:6]`.", ["My *file*.pdf"]);
	assert.equal(
		out.markdown,
		"# T\n\n**Bold** [1] and `[citation:6]`.\n\n---\n\nSources\n\n- [1] My \\*file\\*.pdf\n"
	);
});

test("an answer without citations has no Sources list", () => {
	const out = render("Just words.");
	assert.equal(out.plain, "Just words.");
	assert.equal(out.markdown, "Just words.");
	assert.doesNotMatch(out.html, /Sources/);
});

test("a marker with no number goes with the space before it", () => {
	const numbering = numberingForCopy(["see [citation:doc-4]. and [citation:9, doc-2]"]);
	assert.equal(
		numberMarkers("see [citation:doc-4]. and [citation:9, doc-2]", numbering),
		"see. and [1]"
	);
});

test("sources the namer cannot name keep their number", async () => {
	const numbering = numberingForCopy(["[citation:1] [citation:https://a.org]"]);
	const failing = async () => {
		throw new Error("offline");
	};
	assert.deepEqual(await namedSources(numbering, failing), [
		{ number: 1, name: UNNAMED_SOURCE },
		{ number: 2, name: UNNAMED_SOURCE },
	]);
	const partial = async () =>
		new Map([["url:https://a.org", { name: "https://a.org", href: "https://a.org" }]]);
	assert.deepEqual(await namedSources(numbering, partial), [
		{ number: 1, name: UNNAMED_SOURCE },
		{ number: 2, name: "https://a.org", href: "https://a.org" },
	]);
});

const FORMATS = { html: "<p><strong>x</strong></p>", plain: "x", markdown: "**x**" };

test("the clipboard gets html and plain text through a ClipboardItem", async () => {
	const written: Record<string, string>[] = [];
	class FakeItem {
		constructor(readonly items: Record<string, Promise<Blob>>) {}
	}
	const ok = await writeClipboard(Promise.resolve(FORMATS), {
		ClipboardItem: FakeItem as unknown as typeof ClipboardItem,
		clipboard: {
			write: async (items) => {
				for (const item of items as unknown as FakeItem[]) {
					const out: Record<string, string> = {};
					for (const [type, blob] of Object.entries(item.items))
						out[type] = await (await blob).text();
					written.push(out);
				}
			},
		},
		execCopy: () => assert.fail("the fallback must not run"),
	});
	assert.equal(ok, true);
	assert.deepEqual(written, [{ "text/html": FORMATS.html, "text/plain": FORMATS.plain }]);
});

test("without ClipboardItem, or when it is refused, the copy command writes both forms", async () => {
	const copied: unknown[] = [];
	const host = { execCopy: (formats: unknown) => copied.push(formats) > 0 };
	assert.equal(await writeClipboard(FORMATS, host), true);
	const refused = {
		...host,
		ClipboardItem: class {} as unknown as typeof ClipboardItem,
		clipboard: { write: async () => Promise.reject(new Error("NotAllowedError")) },
	};
	assert.equal(await writeClipboard(FORMATS, refused), true);
	assert.deepEqual(copied, [FORMATS, FORMATS]);
});

test("a copy whose content could not be built reports failure", async () => {
	assert.equal(
		await writeClipboard(Promise.reject(new Error("x")), {
			execCopy: () => assert.fail("no copy"),
		}),
		false
	);
});
