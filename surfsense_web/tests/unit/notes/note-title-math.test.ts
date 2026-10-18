import assert from "node:assert/strict";
import test from "node:test";
import { MarkdownPlugin, remarkMdx } from "@platejs/markdown";
import { createSlateEditor } from "platejs";
import remarkGfm from "remark-gfm";
import { chatMathAsDollars, remarkChatMath } from "@/components/editor/utils/chat-math";
import { escapeMdxExpressions } from "@/components/editor/utils/escape-mdx";
import { safeDeserializeMarkdown } from "@/components/editor/utils/safe-deserialize";
import { noteFromAnswer } from "@/features/notebooks/lib/note-from-answer";
import { notePreview } from "@/features/notebooks/lib/note-preview";
import { MATH_TEXT_MAX_CHARS, mathAsText, texAsText } from "@/lib/markdown/math-as-text";
import { plainText } from "@/lib/markdown/plain-text";

// The answer from the owner's screenshot (2026-10-10): its saved note was
// titled "…Pearson's \(r = 0.463\), statistical…".
const PEARSON =
	"The correlation was moderate and positive: Pearson's \\(r = 0.463\\), statistically " +
	"significant (\\(p < 0.001\\)) [citation:12].\n\n\\[\nr = \\frac{\\sum (x - \\bar{x})(y - \\bar{y})}{n s_x s_y}\n\\]";

test("a note saved from an answer is titled with its maths as words", () => {
	const note = noteFromAnswer(PEARSON);
	assert.equal(
		note.title,
		"The correlation was moderate and positive: Pearson's r = 0.463, statistically s…"
	);
	assert.equal(note.title.length, 80);
	assert.doesNotMatch(note.title, /\\[()[\]]|\$/);
	// The note keeps the answer's maths for the editor to draw.
	assert.match(note.content, /Pearson's \\\(r = 0\.463\\\)/);
});

test("a title cut from a formula-first or quoted answer reads as text", () => {
	assert.equal(noteFromAnswer("\\[ \\chi^2 = 4.2 \\]\n\nSo it is.").title, "χ² = 4.2");
	assert.equal(noteFromAnswer("## Result: $$R^2 = 0.21$$").title, "Result: R² = 0.21");
	assert.equal(
		noteFromAnswer("> **Odds** of $\\beta_1$ [here](https://x.org)").title,
		"Odds of β₁ here"
	);
	assert.equal(noteFromAnswer("- First \\(a\\) - then \\(b\\)").title, "First a - then b");
	assert.equal(
		noteFromAnswer("It costs $3 and $5, not \\$4.").title,
		"It costs $3 and $5, not $4."
	);
});

test("TeX becomes Unicode where it can, letters where it can't", () => {
	const cases: [string, string][] = [
		["r = 0.463", "r = 0.463"],
		["p < 0.001", "p < 0.001"],
		["\\alpha \\le 0.05", "α ≤ 0.05"],
		["\\mu \\pm 2\\sigma", "μ ± 2σ"],
		["x^2 + y^{-1}", "x² + y⁻¹"],
		["e^{i\\pi}", "e^(iπ)"],
		["\\beta_1, x_{max}, p_{adj}", "β₁, xₘₐₓ, p_adj"],
		["\\frac{1}{2}", "1/2"],
		["\\frac{a+b}{n - 1}", "(a+b)/(n - 1)"],
		["\\sqrt{2}, \\sqrt{n+1}, \\sqrt[3]{8}", "√2, √(n+1), ∛8"],
		["\\bar{x} \\hat{\\beta}", "x̄ β̂"],
		["37^\\circ\\text{C}", "37°C"],
		["\\mathrm{OR} = 1.8\\,\\text{(95\\% CI)}", "OR = 1.8 (95% CI)"],
		["\\left( \\frac{a}{b} \\right)", "( a/b )"],
		["\\mathcal{L}(\\theta)", "L(θ)"],
		["\\begin{pmatrix} a & b \\end{pmatrix}", "a b"],
		["\\unknown{x}", "x"],
		["\\frac{1}", "1/"],
		["\\constructor", ""],
	];
	for (const [tex, words] of cases) assert.equal(texAsText(tex), words, tex);
});

test("every form of maths is found; money and lone delimiters are not maths", () => {
	assert.equal(mathAsText("a $$x^2$$ b"), "a x² b");
	assert.equal(mathAsText("a \\[x\\] b \\(y\\) c"), "a x b y c");
	assert.equal(mathAsText("\\begin{equation}E = mc^2\\end{equation}"), "E = mc²");
	assert.equal(mathAsText("where $\\alpha$ is"), "where α is");
	assert.equal(mathAsText("from $5 to $10"), "from $5 to $10");
	assert.equal(mathAsText("between $5-$10 or $ 3 $"), "between $5-$10 or $ 3 $");
	assert.equal(mathAsText("Pearson's \\(r = 0.4…"), "Pearson's r = 0.4…");
	// What runs outside the maths does not reach into it.
	assert.equal(
		mathAsText("_a_ \\(x_1\\)", (text) => text.replace(/_/g, "")),
		"a x₁"
	);
});

test("Markdown reads as its words; a sign between words stays", () => {
	assert.equal(plainText("> **Odds** of $\\beta_1$ [here](https://x.org)"), "Odds of β₁ here");
	assert.equal(plainText("p > 0.05 > q"), "p > 0.05 > q");
});

test("a note's opening words show its maths as text", () => {
	assert.equal(
		notePreview("# Result\n\nPearson's \\(r = 0.463\\), **strong**"),
		"Result Pearson's r = 0.463, strong"
	);
});

/** A note read into the editor as the Notes page reads it, then written back. */
function roundTrip(markdown: string) {
	const editor = createSlateEditor({
		plugins: [
			MarkdownPlugin.configure({
				options: { remarkPlugins: [remarkGfm, remarkChatMath, remarkMdx] },
			}),
		],
	});
	const value = safeDeserializeMarkdown(
		editor as never,
		escapeMdxExpressions(chatMathAsDollars(markdown)),
		remarkChatMath
	);
	return { value, markdown: editor.getApi(MarkdownPlugin).markdown.serialize({ value }) };
}

test("the note editor draws an answer's maths and keeps them when it saves", () => {
	const { value, markdown } = roundTrip(
		"Pearson's \\(r = 0.463\\), costs $5 and $6.\n\n\\[\n\\frac{a}{b}\n\\]"
	);
	const text = JSON.stringify(value);
	assert.match(text, /"texExpression":"r = 0.463","type":"inline_equation"/);
	assert.match(text, /"texExpression":"\\\\frac\{a\}\{b\}","type":"equation"/);
	assert.match(text, /costs \$5 and \$6\./);
	assert.equal(markdown, "Pearson's $$r = 0.463$$, costs $5 and $6.\n\n$$\n\\frac{a}{b}\n$$\n");
	// Written back, it reads the same again.
	assert.equal(roundTrip(markdown).markdown, markdown);
});

test("code keeps its backslashes", () => {
	assert.equal(chatMathAsDollars("`\\(x\\)` and \\(y\\)"), "`\\(x\\)` and $$y$$");
});

test("a crafted note body is titled and previewed in bounded time", () => {
	// Opening delimiters with no closing one: before the cap each scanned to
	// the end, so 200,000 characters of them held the tab for about 4 s and
	// 400,000 for 18 s.
	for (const body of ["\\[".repeat(100_000), "\\(a".repeat(70_000)]) {
		const started = performance.now();
		noteFromAnswer(body);
		notePreview(body);
		assert.ok(performance.now() - started < 1000);
	}
});

test("only the opening of a long note is read", () => {
	const body = `Results \\(r = 0.463\\)\n\n${"x".repeat(MATH_TEXT_MAX_CHARS * 50)} $\\alpha$`;
	assert.equal(noteFromAnswer(body).title, "Results r = 0.463");
	assert.ok(mathAsText(body).length <= MATH_TEXT_MAX_CHARS);
	assert.doesNotMatch(mathAsText(body), /α/);
});

// A link or image longer than the cap at the top of a note: the cut fell
// inside it and the title was the markup. The backend's test_readable_text.py
// has the same cases.
const DATA_IMAGE = `data:image/png;base64,${"iVBORw0KGgo".repeat(400)}`;

test("a note opening with a long image or link is titled by its words", () => {
	assert.ok(DATA_IMAGE.length > MATH_TEXT_MAX_CHARS);
	assert.equal(noteFromAnswer(`![](${DATA_IMAGE})\n\nWard round notes`).title, "Ward round notes");
	assert.equal(noteFromAnswer(`![Chest X-ray](${DATA_IMAGE})\nmore`).title, "Chest X-ray");
	assert.equal(notePreview(`![](${DATA_IMAGE})\n\nWard round notes`), "Ward round notes");
	const url = `https://example.org/${"a".repeat(MATH_TEXT_MAX_CHARS * 2)}`;
	assert.equal(noteFromAnswer(`[The guideline](${url}) says`).title, "The guideline says");
	const label = "words ".repeat(500);
	assert.doesNotMatch(noteFromAnswer(`[${label}](https://x.org)`).title, /\]\(/);
});

test("links are found in time, and maths in brackets stays maths", () => {
	const started = performance.now();
	for (const unit of ["[", "![", "](", "[a](", "[a]"]) {
		noteFromAnswer(unit.repeat(100_000));
		notePreview(unit.repeat(100_000));
	}
	assert.ok(performance.now() - started < 1000);
	assert.equal(plainText("\\[x^2\\](1) holds"), "x²(1) holds");
});
