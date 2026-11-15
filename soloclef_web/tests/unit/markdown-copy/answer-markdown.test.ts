import assert from "node:assert/strict";
import test from "node:test";
import { normalizeAnswerMarkdown } from "@/lib/markdown/answer-markdown";

// Run with: pnpm exec tsx --test tests/unit/markdown-copy/answer-markdown.test.ts

test("a level-2 heading stays a level-2 heading", () => {
	// Regression: the gluing fix matched inside "## X" and made it "#\n\n# X".
	assert.equal(
		normalizeAnswerMarkdown("Intro\n\n## Results\n\ntext"),
		"Intro\n\n## Results\n\ntext"
	);
	assert.equal(normalizeAnswerMarkdown("### Three"), "### Three");
});

test("a heading glued to the sentence before it gets its own line", () => {
	assert.equal(normalizeAnswerMarkdown("done.## Next"), "done.\n\n## Next");
	assert.equal(normalizeAnswerMarkdown("done.# One"), "done.\n\n# One");
});

test("a single # in prose stays text", () => {
	assert.equal(normalizeAnswerMarkdown("C# is fine"), "C# is fine");
});

test("every form of maths becomes $$...$$", () => {
	assert.equal(normalizeAnswerMarkdown("\\(a\\) and \\[b\\]"), "$$a$$ and $$b$$");
});
