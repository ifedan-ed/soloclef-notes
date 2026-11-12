/**
 * The maker's mark for a proxy model name, read from the family word in the
 * name itself ("claude-…", "gpt-…"). The proxy's list carries names and
 * nothing else, so there is no provider field to read. A name that holds no
 * known family gets the neutral chip icon: a mark is decoration, and a
 * wrong one would say something untrue about where a message goes.
 *
 * Checked in order, so a routed name such as "openrouter/anthropic/claude-x"
 * shows the model's maker, not the router.
 */
const FAMILIES: ReadonlyArray<readonly [RegExp, string]> = [
	[/claude|anthropic/, "ANTHROPIC"],
	[/gemini|gemma/, "GOOGLE"],
	[/deepseek/, "DEEPSEEK"],
	[/mistral|mixtral|codestral/, "MISTRAL"],
	[/grok/, "XAI"],
	[/qwen/, "ALIBABA_QWEN"],
	[/kimi|moonshot/, "MOONSHOT"],
	[/glm|zhipu/, "ZHIPU"],
	[/minimax/, "MINIMAX"],
	[/sonar|perplexity/, "PERPLEXITY"],
	[/gpt|openai|(^|[^a-z])o[134](-|$)/, "OPENAI"],
];

/** The provider-icon key for a model name, or null when the name names no known family. */
export function modelFamily(name: string): string | null {
	const lower = name.toLowerCase();
	return FAMILIES.find(([pattern]) => pattern.test(lower))?.[1] ?? null;
}
