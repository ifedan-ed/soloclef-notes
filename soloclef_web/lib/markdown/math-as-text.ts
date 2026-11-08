// Maths as the words it shows, for text that cannot draw it: a note's title
// and the opening words under it. Chat answers write TeX between \( \) or
// \[ \] (sometimes $$ $$ or single dollars), and a title cut from one kept it
// raw: "Pearson's \(r = 0.463\)". Here each formula becomes plain text:
// common TeX as Unicode (r^2 → r², \alpha → α, \le → ≤, \frac{a}{b} → a/b,
// \text{kg} → kg), anything else as its letters with the commands and braces
// taken out. A single dollar is maths only as pandoc reads it (no space after
// the opening one, none before the closing one, no digit after it), so
// "$3 and $5" stays money. Delimiters left without their pair (a title cut in
// the middle of a formula) are dropped.
//
// The backend has the same rules (app/workspace_notes/readable_text.py) and
// the phone app a copy held to this file (soloclef_mobile/src/lib/render/
// mathAsText.ts); a change here changes them too.

const GREEK: Record<string, string> = {
	alpha: "α",
	beta: "β",
	gamma: "γ",
	delta: "δ",
	epsilon: "ε",
	varepsilon: "ε",
	zeta: "ζ",
	eta: "η",
	theta: "θ",
	vartheta: "ϑ",
	iota: "ι",
	kappa: "κ",
	lambda: "λ",
	mu: "μ",
	nu: "ν",
	xi: "ξ",
	pi: "π",
	rho: "ρ",
	sigma: "σ",
	tau: "τ",
	upsilon: "υ",
	phi: "φ",
	varphi: "φ",
	chi: "χ",
	psi: "ψ",
	omega: "ω",
	Gamma: "Γ",
	Delta: "Δ",
	Theta: "Θ",
	Lambda: "Λ",
	Xi: "Ξ",
	Pi: "Π",
	Sigma: "Σ",
	Upsilon: "Υ",
	Phi: "Φ",
	Psi: "Ψ",
	Omega: "Ω",
};

const SYMBOLS: Record<string, string> = {
	times: "×",
	cdot: "·",
	div: "÷",
	pm: "±",
	mp: "∓",
	le: "≤",
	leq: "≤",
	ge: "≥",
	geq: "≥",
	lt: "<",
	gt: ">",
	neq: "≠",
	ne: "≠",
	approx: "≈",
	sim: "∼",
	simeq: "≃",
	equiv: "≡",
	propto: "∝",
	ll: "≪",
	gg: "≫",
	infty: "∞",
	to: "→",
	rightarrow: "→",
	leftarrow: "←",
	leftrightarrow: "↔",
	Rightarrow: "⇒",
	implies: "⇒",
	Leftrightarrow: "⇔",
	iff: "⇔",
	uparrow: "↑",
	downarrow: "↓",
	degree: "°",
	circ: "°",
	partial: "∂",
	nabla: "∇",
	sum: "∑",
	int: "∫",
	prod: "∏",
	in: "∈",
	notin: "∉",
	cup: "∪",
	cap: "∩",
	subset: "⊂",
	forall: "∀",
	exists: "∃",
	neg: "¬",
	land: "∧",
	lor: "∨",
	mid: "|",
	vert: "|",
	lvert: "|",
	rvert: "|",
	langle: "⟨",
	rangle: "⟩",
	ldots: "…",
	cdots: "⋯",
	dots: "…",
	prime: "′",
	quad: " ",
	qquad: " ",
	log: "log",
	ln: "ln",
	exp: "exp",
	sin: "sin",
	cos: "cos",
	tan: "tan",
	min: "min",
	max: "max",
	lim: "lim",
};

const SUPERSCRIPT: Record<string, string> = {
	"0": "⁰",
	"1": "¹",
	"2": "²",
	"3": "³",
	"4": "⁴",
	"5": "⁵",
	"6": "⁶",
	"7": "⁷",
	"8": "⁸",
	"9": "⁹",
	"+": "⁺",
	"-": "⁻",
	"−": "⁻",
	"=": "⁼",
	"(": "⁽",
	")": "⁾",
	n: "ⁿ",
	i: "ⁱ",
	x: "ˣ",
	y: "ʸ",
	T: "ᵀ",
	"°": "°",
};

const SUBSCRIPT: Record<string, string> = {
	"0": "₀",
	"1": "₁",
	"2": "₂",
	"3": "₃",
	"4": "₄",
	"5": "₅",
	"6": "₆",
	"7": "₇",
	"8": "₈",
	"9": "₉",
	"+": "₊",
	"-": "₋",
	"−": "₋",
	"=": "₌",
	"(": "₍",
	")": "₎",
	a: "ₐ",
	e: "ₑ",
	o: "ₒ",
	x: "ₓ",
	i: "ᵢ",
	r: "ᵣ",
	u: "ᵤ",
	v: "ᵥ",
	h: "ₕ",
	k: "ₖ",
	l: "ₗ",
	m: "ₘ",
	n: "ₙ",
	p: "ₚ",
	s: "ₛ",
	t: "ₜ",
	j: "ⱼ",
};

// A mark over one letter: \bar{x} → x̄.
const ACCENTS: Record<string, string> = {
	bar: "̄",
	overline: "̄",
	hat: "̂",
	widehat: "̂",
	tilde: "̃",
	widetilde: "̃",
	vec: "⃗",
	dot: "̇",
	ddot: "̈",
};

// Commands whose argument is words, kept as they are.
const TEXT = new Set([
	"text",
	"textrm",
	"textit",
	"textbf",
	"mathrm",
	"mathit",
	"mathbf",
	"mathsf",
	"operatorname",
	"mbox",
]);

const FRACTIONS = new Set(["frac", "dfrac", "tfrac"]);
const SPACES = new Set(["\\", ",", ";", ":", " "]);
const COMMAND = /^\\([A-Za-z]+|[\s\S]?)/;

/** A table's own entry (a command named "constructor" is not one). */
function own(table: Record<string, string>, key: string): string | undefined {
	return Object.hasOwn(table, key) ? table[key] : undefined;
}

/** The argument at `i` (a {group}, a \command or one character) and the index after it. */
function argument(src: string, from: number): [string, number] {
	let i = from;
	while (src[i] === " ") i++;
	if (src[i] === "{") {
		let depth = 0;
		for (let j = i; j < src.length; j++) {
			if (src[j] === "{") depth++;
			else if (src[j] === "}") {
				depth--;
				if (depth === 0) return [src.slice(i + 1, j), j + 1];
			}
		}
		return [src.slice(i + 1), src.length];
	}
	if (src[i] === "\\") {
		const command = COMMAND.exec(src.slice(i))?.[0] ?? "\\";
		return [command, i + command.length];
	}
	return [src[i] ?? "", i + 1];
}

/** A fraction's part, bracketed when it is more than one term: (a+b)/2. */
function term(text: string): string {
	return /[\s+\-−×·=<>/]/.test(text) ? `(${text})` : text;
}

function scripted(text: string, table: Record<string, string>, mark: string): string {
	const chars = [...text];
	if (chars.length > 0 && chars.every((ch) => own(table, ch) !== undefined)) {
		return chars.map((ch) => own(table, ch)).join("");
	}
	return mark === "^" && chars.length > 1 ? `^(${text})` : `${mark}${text}`;
}

function words(src: string): string {
	let out = "";
	let i = 0;
	while (i < src.length) {
		const ch = src[i];
		if (ch === "\\") {
			const command = COMMAND.exec(src.slice(i))?.[0] ?? "\\";
			const name = command.slice(1);
			i += command.length;
			if (FRACTIONS.has(name)) {
				const [a, afterA] = argument(src, i);
				const [b, afterB] = argument(src, afterA);
				out += `${term(words(a))}/${term(words(b))}`;
				i = afterB;
			} else if (name === "sqrt") {
				let root = "√";
				if (src[i] === "[") {
					const close = src.indexOf("]", i);
					const degree = close < 0 ? "" : src.slice(i + 1, close).trim();
					root = degree === "3" ? "∛" : degree === "4" ? "∜" : "√";
					i = close < 0 ? i : close + 1;
				}
				const [x, after] = argument(src, i);
				const inner = words(x);
				out += [...inner].length > 1 ? `${root}(${inner})` : `${root}${inner}`;
				i = after;
			} else if (TEXT.has(name)) {
				const [x, after] = argument(src, i);
				out += words(x);
				i = after;
			} else if (own(ACCENTS, name) !== undefined) {
				const [x, after] = argument(src, i);
				const inner = words(x);
				out += [...inner].length === 1 ? inner + own(ACCENTS, name) : inner;
				i = after;
			} else if (name === "begin" || name === "end") {
				i = argument(src, i)[1];
			} else if (name === "left" || name === "right") {
				// The bracket after it stays; \left. is no bracket.
				if (src[i] === ".") i++;
			} else if (own(GREEK, name) !== undefined) out += own(GREEK, name);
			else if (own(SYMBOLS, name) !== undefined) out += own(SYMBOLS, name);
			else if (SPACES.has(name)) out += " ";
			else if (!/^[A-Za-z]+$/.test(name) && name !== "!") {
				// An escaped character (\%, \$, \{, \_) is itself.
				out += name;
			}
			// Any other command is dropped; what it applies to stays.
		} else if (ch === "^" || ch === "_") {
			const [x, after] = argument(src, i + 1);
			out += scripted(words(x), ch === "^" ? SUPERSCRIPT : SUBSCRIPT, ch);
			i = after;
		} else if (ch === "{" || ch === "}") {
			i++;
		} else if (ch === "~" || ch === "&") {
			out += " ";
			i++;
		} else {
			out += ch;
			i++;
		}
	}
	return out;
}

/** One formula's TeX as plain text. Never fails: what it can't turn into a symbol stays as letters. */
export function texAsText(tex: string): string {
	return words(tex).replace(/\s+/g, " ").trim();
}

// The forms chat answers write maths in. Groups: 1 $$…$$, 2 \[…\], 3 \(…\),
// 4–5 \begin{equation|displaymath|math}…\end{…}, 6 a single-dollar formula.
const MATH = new RegExp(
	[
		String.raw`\$\$([\s\S]+?)\$\$`,
		String.raw`\\\[([\s\S]+?)\\\]`,
		String.raw`\\\(([\s\S]+?)\\\)`,
		String.raw`\\begin\{(equation|displaymath|math)\}([\s\S]*?)\\end\{\4\}`,
		String.raw`(?<![\\$])\$(?![\s$])((?:[^$\n\\]|\\.)+?)(?<!\s)\$(?![\d$])`,
	].join("|"),
	"g"
);

// A delimiter whose pair is not there (a cut title): dropped.
const STRAY = /\\[()[\]]|\$\$|\\(?:begin|end)\{(?:equation|displaymath|math)\}/g;
// Where a formula stood while `outside` runs: private-use characters, which
// no Markdown pattern matches.
const HELD = /\ue000(\d+)\ue001/g;

/**
 * The most of a text mathAsText reads: 2,000 characters; the rest is cut.
 * MATH scans from every opening delimiter to the end of the text when no
 * closing one follows, so its time grows with the square of the length: a
 * 400,000-character note of bare \[ held the tab for 18 s when its row's
 * preview was drawn. Every caller shows only the opening words (a title, a
 * preview), so 2,000 is plenty. The backend's MATH_TEXT_MAX_CHARS is the
 * same, and so must the phone app's copy be.
 */
export const MATH_TEXT_MAX_CHARS = 2000;

/**
 * The first MATH_TEXT_MAX_CHARS of `text`, each formula as plain text
 * (texAsText). `outside` (a caller's Markdown clean-up) runs on the text
 * with the formulas held out of its reach, since `_` and `*` mean something
 * else in maths; it still sees where each line starts.
 */
export function mathAsText(text: string, outside: (text: string) => string = (t) => t): string {
	const formulas: string[] = [];
	const held = text
		.slice(0, MATH_TEXT_MAX_CHARS)
		.replace(/[\ue000\ue001]/g, "")
		.replace(MATH, (_whole: string, ...groups: (string | undefined)[]) => {
			const [dollars, display, inline, , environment, single] = groups;
			formulas.push(texAsText(dollars ?? display ?? inline ?? environment ?? single ?? ""));
			return `\ue000${formulas.length - 1}\ue001`;
		})
		.replace(STRAY, "");
	return outside(held).replace(HELD, (_held, n: string) => formulas[Number(n)] ?? "");
}
