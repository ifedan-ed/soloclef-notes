// The layout check of a scene's last frame: what the still review's vision
// model was asked to judge from pictures, measured instead from the page
// Chrome laid out. The scene is model-written React, but once Chrome has
// laid it out every box, line of text, colour and paint order is known
// exactly, so these are rules, not opinions: the same scene always gets the
// same findings, and a finding names the text it is about.
//
// It is the slide check of PedsHub Tools (tools-go internal/pptxcheck:
// every word inside its box, no word over another, nothing hidden under a
// shape drawn after it; internal/pptxrender/contrast.go: WCAG contrast),
// carried from a deck's boxes to a page's.
//
// Speed: one pass over the scene's text nodes and elements. Hit tests
// (elementsFromPoint) are made only where a painted element's box meets a
// line of text, so a clean scene costs a few milliseconds.

// The frame, in CSS pixels: the composition's size (Root.tsx).
const FRAME_WIDTH = 1920;
const FRAME_HEIGHT = 1080;
// SAFE_MARGIN keeps text off the frame's edge, where players draw their
// controls and televisions overscan. The scene rules ask for about 100 px of
// padding; 40 px is the line that is never right to cross.
const SAFE_MARGIN = 40;
// SLACK absorbs sub-pixel rounding and italic overhang: less than any
// overflow a viewer could see.
const SLACK = 2;
// MIN_TEXT_PX is the scene rules' smallest type ("nothing under 30 px").
const MIN_TEXT_PX = 30;
// Contrast, as Tools holds a slide to it (WCAG 2.1 AA): 4.5:1 for ordinary
// text, 3:1 for large. WCAG's large is 18 pt (24 CSS px) on a page read at
// arm's length; Tools uses 24 pt on a 960 pt-wide slide, 2.5% of the width.
// The same share of a 1920 px frame is 48 px, which is what a video shown
// on a phone or across a room needs. Bold text counts as large from 14/18
// of that, as in WCAG.
const LARGE_TEXT_PX = 48;
const LARGE_BOLD_TEXT_PX = (LARGE_TEXT_PX * 14) / 18;
const CONTRAST_BODY = 4.5;
const CONTRAST_LARGE = 3;
// A text of at most three characters set at 200 px or more is ornament (a
// big numeral or quotation mark behind the content), as Tools treats it; it
// is held to no overlap or contrast rule.
const ORNAMENT_PX = 200;
// Overlap: two texts whose line boxes share more than this part of the
// smaller one are drawn over each other (Tools' Overflows uses the same).
const OVERLAP_SHARE = 0.25;
// A text whose opacity is under this at the scene's end cannot be read.
const INVISIBLE_OPACITY = 0.05;
// A cover is reported once it hides at least this many sampled points of a
// line, so a shape that grazes one pixel of a glyph's edge is not a finding.
const MIN_COVERED_POINTS = 2;
// The hit tests per text line and covering element are capped: the grid
// widens instead, which still finds any line or shape wider than the step.
const MAX_SAMPLES = 400;
// A scene reports at most this many findings: a repair needs to know what
// is wrong, not every line of a scene that is wrong everywhere.
const MAX_FINDINGS = 12;

type Box = {left: number; top: number; right: number; bottom: number};
type Rgba = {r: number; g: number; b: number; a: number};

// One run of text as Chrome set it: a text node and its line boxes.
type TextRun = {
  node: Text;
  element: Element;
  text: string;
  size: number;
  bold: boolean;
  opacity: number;
  lines: Box[];
  ornament: boolean;
};

const area = (box: Box) =>
  Math.max(0, box.right - box.left) * Math.max(0, box.bottom - box.top);

const intersection = (a: Box, b: Box): Box => ({
  left: Math.max(a.left, b.left),
  top: Math.max(a.top, b.top),
  right: Math.min(a.right, b.right),
  bottom: Math.min(a.bottom, b.bottom),
});

const boxOf = (rect: DOMRect): Box => ({
  left: rect.left,
  top: rect.top,
  right: rect.right,
  bottom: rect.bottom,
});

const inside = (inner: Box, outer: Box, horizontal: number, vertical: number) =>
  inner.left >= outer.left - horizontal &&
  inner.right <= outer.right + horizontal &&
  inner.top >= outer.top - vertical &&
  inner.bottom <= outer.bottom + vertical;

// quoted is a text as a finding names it: its first words, on one line.
const quoted = (text: string) => {
  const flat = text.replace(/\s+/g, " ").trim();
  return `"${flat.length > 50 ? `${flat.slice(0, 49)}…` : flat}"`;
};

// Colours are read by drawing them: a 1x1 canvas resolves every CSS colour
// syntax Chrome accepts (rgb, hsl, oklch, color(), names) to sRGB, so no
// parser here can fall behind what a scene may write.
const colourCache = new Map<string, Rgba | null>();
let colourContext: OffscreenCanvasRenderingContext2D | null = null;
const colourOf = (css: string): Rgba | null => {
  const cached = colourCache.get(css);
  if (cached !== undefined) return cached;
  colourContext ??= new OffscreenCanvas(1, 1).getContext("2d", {
    willReadFrequently: true,
  });
  let colour: Rgba | null = null;
  if (colourContext) {
    colourContext.clearRect(0, 0, 1, 1);
    colourContext.fillStyle = "rgba(0, 0, 0, 0)";
    colourContext.fillStyle = css;
    colourContext.fillRect(0, 0, 1, 1);
    const [r, g, b, a] = colourContext.getImageData(0, 0, 1, 1).data;
    colour = {r, g, b, a: a / 255};
  }
  colourCache.set(css, colour);
  return colour;
};

// WCAG 2.1 relative luminance and contrast ratio.
const luminance = ({r, g, b}: Rgba) => {
  const linear = (channel: number) => {
    const c = channel / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
};
const contrast = (a: Rgba, b: Rgba) => {
  const [high, low] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (high + 0.05) / (low + 0.05);
};

// over is colour painted with its alpha onto an opaque ground.
const over = (colour: Rgba, alpha: number, ground: Rgba): Rgba => ({
  r: colour.r * alpha + ground.r * (1 - alpha),
  g: colour.g * alpha + ground.g * (1 - alpha),
  b: colour.b * alpha + ground.b * (1 - alpha),
  a: 1,
});

// opacityOf is an element's opacity as painted: its own times every
// ancestor's, up to the scene's root.
const opacityCache = new Map<Element, number>();
const opacityOf = (element: Element, root: Element): number => {
  const cached = opacityCache.get(element);
  if (cached !== undefined) return cached;
  let value = Number(getComputedStyle(element).opacity);
  if (!Number.isFinite(value)) value = 1;
  if (element !== root && element.parentElement) {
    value *= opacityOf(element.parentElement, root);
  }
  opacityCache.set(element, value);
  return value;
};

// scaleOf is how much transforms and an SVG's viewBox enlarge an element's
// type: its computed font-size is in its own units, not the frame's pixels.
const scaleOf = (element: Element): number => {
  if (element instanceof SVGGraphicsElement) {
    const matrix = element.getScreenCTM();
    return matrix ? Math.hypot(matrix.a, matrix.b) : 1;
  }
  if (element instanceof HTMLElement && element.offsetHeight > 0) {
    const drawn = element.getBoundingClientRect().height;
    return drawn > 0 ? drawn / element.offsetHeight : 1;
  }
  return 1;
};

const isMedia = (element: Element) =>
  element instanceof HTMLImageElement ||
  element instanceof HTMLVideoElement ||
  element instanceof HTMLCanvasElement ||
  element instanceof SVGImageElement;

// paints says whether an element puts colour on the frame itself, apart
// from its text and children: a background, a border, a picture, or an SVG
// shape's fill or stroke. A transparent layout box over the text does not
// cover it, however large it is.
const paints = (element: Element): boolean => {
  if (isMedia(element)) return true;
  const style = getComputedStyle(element);
  if (style.visibility !== "visible") return false;
  if (element instanceof SVGGeometryElement) {
    const fill = style.fill !== "none" && (colourOf(style.fill)?.a ?? 1) > 0;
    const stroke =
      style.stroke !== "none" &&
      parseFloat(style.strokeWidth) > 0 &&
      (colourOf(style.stroke)?.a ?? 1) > 0;
    return fill || stroke;
  }
  if (element instanceof SVGElement) return false;
  if (style.backgroundImage !== "none") return true;
  if ((colourOf(style.backgroundColor)?.a ?? 0) > 0) return true;
  return borderWidths(style).some((width) => width > 0);
};

const borderWidths = (style: CSSStyleDeclaration) =>
  [
    [style.borderTopWidth, style.borderTopStyle, style.borderTopColor],
    [style.borderRightWidth, style.borderRightStyle, style.borderRightColor],
    [style.borderBottomWidth, style.borderBottomStyle, style.borderBottomColor],
    [style.borderLeftWidth, style.borderLeftStyle, style.borderLeftColor],
  ].map(([width, kind, colour]) =>
    kind === "none" || kind === "hidden" || (colourOf(colour)?.a ?? 0) === 0
      ? 0
      : parseFloat(width) || 0,
  );

// paintsAt says whether a painting element puts colour at a point: an
// element with only a border paints its edge, not its middle.
const paintsAt = (element: Element, x: number, y: number): boolean => {
  if (isMedia(element) || element instanceof SVGElement) return true;
  const style = getComputedStyle(element);
  if (style.backgroundImage !== "none") return true;
  if ((colourOf(style.backgroundColor)?.a ?? 0) > 0) return true;
  const [top, right, bottom, left] = borderWidths(style);
  const box = element.getBoundingClientRect();
  return (
    y < box.top + top ||
    x > box.right - right ||
    y > box.bottom - bottom ||
    x < box.left + left
  );
};

// What a painted element is, in a finding.
const describe = (element: Element) => {
  if (
    element instanceof SVGLineElement ||
    element instanceof SVGPolylineElement ||
    (element instanceof SVGGeometryElement &&
      getComputedStyle(element).fill === "none")
  ) {
    return "a line";
  }
  if (isMedia(element)) return "a picture";
  return "a shape";
};

// lineBox is a line of text's box as its line-height sets it. Chrome
// reports a text's content area, which is taller than the line when the
// line-height is tight (a 100 px heading at line-height 1 reports about
// 120 px), so consecutive lines would seem to overlap and to spill out of
// a box they fit.
const lineBox = (rect: DOMRect, element: Element): Box => {
  const box = boxOf(rect);
  const lineHeight = parseFloat(getComputedStyle(element).lineHeight);
  if (!Number.isFinite(lineHeight) || element instanceof SVGElement) return box;
  const drawn = lineHeight * scaleOf(element);
  const excess = (box.bottom - box.top - drawn) / 2;
  if (excess <= 0) return box;
  return {...box, top: box.top + excess, bottom: box.bottom - excess};
};

// boxElement is the box a text is set in: its nearest ancestor that is not
// an inline element. An SVG text has no box of its own; only the SVG's
// viewport clips it.
const boxElement = (element: Element): Element | null => {
  for (let at: Element | null = element; at; at = at.parentElement) {
    if (at instanceof SVGElement) return null;
    const display = getComputedStyle(at).display;
    if (display !== "inline" && display !== "contents") return at;
  }
  return null;
};

// collectText is every visible run of text in the scene.
const collectText = (root: Element): TextRun[] => {
  const runs: TextRun[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent ?? "";
    const element = node.parentElement;
    if (!element || !text.trim()) continue;
    if (element.closest("style, script, title, desc, defs")) continue;
    const style = getComputedStyle(element);
    if (style.visibility !== "visible") continue;
    range.selectNodeContents(node);
    const lines = Array.from(range.getClientRects())
      .filter((rect) => rect.width > 0 && rect.height > 0)
      .map((rect) => lineBox(rect, element));
    if (lines.length === 0) continue;
    const size = parseFloat(style.fontSize) * scaleOf(element);
    runs.push({
      node: node as Text,
      element,
      text,
      size,
      bold: Number(style.fontWeight) >= 700,
      opacity: opacityOf(element, root),
      lines,
      ornament: text.trim().length <= 3 && size >= ORNAMENT_PX,
    });
  }
  return runs;
};

// related says whether two elements are one the other's ancestor or the
// same: a text's own box and its parents' backgrounds are what it sits on,
// not what covers it.
const related = (a: Element, b: Element) => a === b || a.contains(b) || b.contains(a);

// textIndex is where, in a hit test's stack (topmost first), the text's own
// element or the nearest of its ancestors lies.
const textIndex = (stack: Element[], element: Element) =>
  stack.findIndex((hit) => related(hit, element));

// covered counts the sampled points of a line where painter is drawn above
// the text.
const covered = (line: Box, run: TextRun, painter: Element, reach: Box): number => {
  // The line's middle band: a shape that only touches the tops of capitals
  // or the tails of descenders is not drawn through the text.
  const band = (line.bottom - line.top) * 0.15;
  const region = intersection({...line, top: line.top + band, bottom: line.bottom - band}, reach);
  if (area(region) <= 0) return 0;
  const width = region.right - region.left;
  const height = region.bottom - region.top;
  const step = Math.max(2, Math.sqrt((width * height) / MAX_SAMPLES));
  let hits = 0;
  for (let y = region.top + step / 2; y < region.bottom; y += step) {
    for (let x = region.left + step / 2; x < region.right; x += step) {
      const stack = document.elementsFromPoint(x, y);
      const painted = stack.indexOf(painter);
      if (painted < 0 || !paintsAt(painter, x, y)) continue;
      const own = textIndex(stack, run.element);
      if (own < 0 || painted < own) hits += 1;
    }
  }
  return hits;
};

// groundOf is the opaque colour a text sits on at a point, or null when
// what is under it is a picture, a gradient or nothing the page paints.
const groundOf = (run: TextRun, x: number, y: number, root: Element): Rgba | null => {
  const stack = document.elementsFromPoint(x, y);
  const own = textIndex(stack, run.element);
  if (own < 0) return null;
  // Translucent backgrounds between the text and the first opaque one,
  // nearest the text first.
  const layers: {colour: Rgba; alpha: number}[] = [];
  for (const element of stack.slice(own)) {
    if (element !== run.element && isMedia(element)) return null;
    if (element instanceof SVGGeometryElement && element !== run.element) {
      if (!paints(element)) continue;
      const fill = colourOf(getComputedStyle(element).fill);
      if (!fill) return null;
      const alpha = fill.a * opacityOf(element, root);
      layers.push({colour: fill, alpha});
      if (alpha >= 0.999) break;
      continue;
    }
    if (element instanceof SVGElement) continue;
    const style = getComputedStyle(element);
    if (style.backgroundImage !== "none") return null;
    const background = colourOf(style.backgroundColor);
    if (!background || background.a === 0) continue;
    const alpha = background.a * opacityOf(element, root);
    layers.push({colour: background, alpha});
    if (alpha >= 0.999) break;
  }
  const bottom = layers.pop();
  // Nothing opaque under the text: the frame is transparent there, and the
  // encoder decides its colour, so the scene has not set what the text is
  // read against. That is the scene's fault, reported by the caller.
  if (!bottom || bottom.alpha < 0.999) return null;
  let ground = bottom.colour;
  while (layers.length > 0) {
    const layer = layers.pop()!;
    ground = over(layer.colour, layer.alpha, ground);
  }
  return ground;
};

// measureScene checks one scene's frame, root being the element the scene
// is drawn in, and returns its findings.
export const measureScene = (root: Element): string[] => {
  opacityCache.clear();
  const findings: string[] = [];
  const seen = new Set<string>();
  const find = (finding: string) => {
    if (!seen.has(finding)) {
      seen.add(finding);
      findings.push(finding);
    }
  };
  const frame: Box = {left: 0, top: 0, right: FRAME_WIDTH, bottom: FRAME_HEIGHT};
  const safe: Box = {
    left: SAFE_MARGIN,
    top: SAFE_MARGIN,
    right: FRAME_WIDTH - SAFE_MARGIN,
    bottom: FRAME_HEIGHT - SAFE_MARGIN,
  };

  const runs = collectText(root);
  const visible = runs.filter((run) => run.opacity >= INVISIBLE_OPACITY);
  const elements = Array.from(root.querySelectorAll("*"));
  const media = elements.filter(
    (element) =>
      (isMedia(element) ||
        (element instanceof SVGSVGElement && !element.ownerSVGElement)) &&
      area(boxOf(element.getBoundingClientRect())) > 0 &&
      opacityOf(element, root) >= INVISIBLE_OPACITY &&
      getComputedStyle(element).visibility === "visible",
  );

  if (visible.length === 0 && media.length === 0) {
    find("shows nothing at its end: no visible text or picture");
  }

  for (const run of runs) {
    const name = quoted(run.text);
    if (run.opacity < INVISIBLE_OPACITY) {
      // The rules keep everything on screen to the scene's end; a text
      // faded out, or never brought in, is content the viewer never reads.
      find(`text ${name} is invisible at the scene's end (opacity ${run.opacity.toFixed(2)})`);
      continue;
    }
    if (run.size < MIN_TEXT_PX - 0.5) {
      find(`text ${name} is ${Math.round(run.size)} px; nothing may be under ${MIN_TEXT_PX} px`);
    }
    const lines = run.lines;
    if (lines.some((line) => !inside(line, frame, SLACK, SLACK))) {
      find(`text ${name} runs off the frame`);
    } else if (lines.some((line) => !inside(line, safe, SLACK, SLACK))) {
      find(`text ${name} is within ${SAFE_MARGIN} px of the frame's edge`);
    }
    // The boxes a text must stay in: the box it is set in; every ancestor
    // that is seen (a card's background or border), since text spilling out
    // of a card is the overflow a viewer sees, even when the transparent
    // box between them grew to hold it; and every ancestor that clips
    // (overflow other than visible, and each nested SVG's viewport), which
    // cuts off what lies outside it.
    const box = boxElement(run.element);
    for (let at: Element | null = box ?? run.element.parentElement; at && at !== root; at = at.parentElement) {
      const style = getComputedStyle(at);
      const clips = style.overflowX !== "visible" || style.overflowY !== "visible";
      if (at !== box && !clips && !paints(at)) continue;
      const bounds = boxOf(at.getBoundingClientRect());
      // A box as large as the frame says nothing the frame's own rule
      // above has not said.
      if (inside(frame, bounds, 0, 0)) continue;
      if (lines.every((line) => inside(line, bounds, SLACK, SLACK))) continue;
      find(
        clips
          ? `text ${name} is cut off by the edge of its container`
          : `text ${name} overflows its box (${Math.round(bounds.right - bounds.left)}x${Math.round(bounds.bottom - bounds.top)} px)`,
      );
      break;
    }
  }

  // Text drawn over text.
  const readable = visible.filter((run) => !run.ornament);
  for (let i = 0; i < readable.length; i += 1) {
    for (let j = i + 1; j < readable.length; j += 1) {
      const a = readable[i];
      const b = readable[j];
      const clash = a.lines.some((one) =>
        b.lines.some((other) => {
          const shared = area(intersection(one, other));
          return shared > OVERLAP_SHARE * Math.min(area(one), area(other));
        }),
      );
      if (clash) find(`texts ${quoted(a.text)} and ${quoted(b.text)} are drawn over each other`);
    }
  }

  // Something painted over text: a line through a label, a shape or a
  // picture on top of words. Only painted elements whose box meets a line
  // of the text are hit-tested, and only where they meet.
  const painters = elements.filter(
    (element) =>
      paints(element) && opacityOf(element, root) >= INVISIBLE_OPACITY,
  );
  for (const run of readable) {
    for (const painter of painters) {
      if (related(painter, run.element)) continue;
      // An SVG line or path has no area of its own: its box is widened by
      // half its stroke so a horizontal or vertical line still has one.
      const rect = boxOf(painter.getBoundingClientRect());
      const half =
        painter instanceof SVGGeometryElement
          ? ((parseFloat(getComputedStyle(painter).strokeWidth) || 0) * scaleOf(painter)) / 2
          : 0;
      const reach = {
        left: rect.left - half,
        top: rect.top - half,
        right: rect.right + half,
        bottom: rect.bottom + half,
      };
      let hits = 0;
      for (const line of run.lines) {
        hits += covered(line, run, painter, reach);
        if (hits >= MIN_COVERED_POINTS) break;
      }
      if (hits >= MIN_COVERED_POINTS) {
        find(`text ${quoted(run.text)} has ${describe(painter)} drawn over it`);
      }
    }
  }

  // Contrast, read where each line's middle is.
  for (const run of readable) {
    const colour = colourOf(getComputedStyle(run.element).color);
    if (!colour) continue;
    const large = run.size >= LARGE_TEXT_PX || (run.bold && run.size >= LARGE_BOLD_TEXT_PX);
    const need = large ? CONTRAST_LARGE : CONTRAST_BODY;
    for (const line of run.lines) {
      const x = (line.left + line.right) / 2;
      const y = (line.top + line.bottom) / 2;
      if (!inside({left: x, top: y, right: x, bottom: y}, frame, 0, 0)) continue;
      const stack = document.elementsFromPoint(x, y);
      const own = textIndex(stack, run.element);
      // Under a picture or gradient the ground is not one colour; the rule
      // cannot judge it and does not pretend to.
      if (own < 0) continue;
      const ground = groundOf(run, x, y, root);
      if (!ground) {
        const underPicture = stack
          .slice(own)
          .some((element) => element !== run.element && (isMedia(element) || getComputedStyle(element).backgroundImage !== "none"));
        if (!underPicture) {
          find(`text ${quoted(run.text)} has no background colour under it; give the scene an opaque background`);
        }
        break;
      }
      const ink = over(colour, colour.a * run.opacity, ground);
      const ratio = contrast(ink, ground);
      if (ratio < need) {
        find(`text ${quoted(run.text)} has contrast ${ratio.toFixed(2)}:1 against its background; it needs ${need}:1`);
        break;
      }
    }
  }

  // Pictures, SVGs, canvases and videos stay inside the frame.
  for (const element of media) {
    if (!inside(boxOf(element.getBoundingClientRect()), frame, SLACK, SLACK)) {
      find(`${describe(element) === "a picture" ? "a picture" : "an SVG drawing"} extends past the frame`);
    }
  }

  return findings.slice(0, MAX_FINDINGS);
};

// The line render.mjs reads the result from (onBrowserLog), and the shape
// of what follows it.
export const LAYOUT_MARK = "SOLOCLEF_LAYOUT ";
export type LayoutReport = {slide_number: number; findings: string[]};
