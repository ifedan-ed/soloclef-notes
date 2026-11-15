// Lists the server routes one phone app build calls, from its source.
//
//   node app-calls.mjs --src <soloclef_mobile/src> --openapi <openapi.json> \
//     --resolutions <resolutions.json> --typescript <path to a typescript package>
//
// Writes the calls as JSON to stdout (take-snapshot.sh saves them as the
// build's calls.json). Exits 1, naming each one, when a path the source
// mentions is neither a call it could read nor in the resolutions file, or
// when a resolution no longer matches anything: the list is only worth
// pinning the server to if nothing the app sends was missed.
//
// Why the TypeScript compiler and not a regular expression: most calls go
// through the app's typed client (`backend.get(P, ...)` with P a path of
// openapi.json), and the path is often a constant, a template over a constant
// (`${NB}/sources`) or a conditional. The checker reads the literal type the
// call was resolved with, so those come out exact. What it cannot read (a
// path handed to a stream, a download, the browser) is a "mention", which a
// person resolves once per build in resolutions.json, with the reason.
//
// Why the whole spec is not used instead: an operation in openapi.json the
// app never calls must not pin the server.
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { parseArgs } from "node:util";

const { values: args } = parseArgs({
  options: {
    src: { type: "string" },
    openapi: { type: "string" },
    resolutions: { type: "string" },
    typescript: { type: "string" },
  },
});
for (const name of ["src", "openapi", "resolutions", "typescript"]) {
  if (!args[name]) {
    console.error(`app-calls: --${name} is required`);
    process.exit(2);
  }
}
const srcDir = path.resolve(args.src);
const ts = createRequire(import.meta.url)(path.resolve(args.typescript));
const spec = JSON.parse(fs.readFileSync(args.openapi, "utf8"));
const resolutions = JSON.parse(fs.readFileSync(args.resolutions, "utf8"));

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE"];
// The typed client's verbs (lib/api/typed.ts), the "loose" ones included.
const VERBS = { get: "GET", post: "POST", put: "PUT", patch: "PATCH", delete: "DELETE", getLoose: "GET", postLoose: "POST", putLoose: "PUT" };
const isServerPath = (text) => /^\/(api|auth|users)(\/|$)/.test(text);

// The source the bundle is built from: tests and declarations are not in it.
function sourceFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) return sourceFiles(full);
    return /\.(ts|tsx)$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name) && !entry.name.endsWith(".d.ts") ? [full] : [];
  });
}
// openapi.d.ts is passed so that the typed client's paths resolve; the
// app's node_modules are not needed for that, and their absence only widens
// types this script does not read.
const program = ts.createProgram([...sourceFiles(srcDir), path.join(srcDir, "lib/api/openapi.d.ts")], {
  strict: true,
  noEmit: true,
  skipLibCheck: true,
  jsx: ts.JsxEmit.ReactNative,
  target: ts.ScriptTarget.ES2022,
  module: ts.ModuleKind.ESNext,
  moduleResolution: ts.ModuleResolutionKind.Bundler,
  baseUrl: path.dirname(srcDir),
  paths: { "@/*": ["./src/*"] },
});
const checker = program.getTypeChecker();

const calls = [];
const consumed = new Set();
const mentions = [];

function where(node) {
  const file = node.getSourceFile();
  return `${path.relative(srcDir, file.fileName)}:${file.getLineAndCharacterOfPosition(node.getStart()).line + 1}`;
}
function stringLiterals(type) {
  const out = [];
  for (const member of type.isUnion() ? type.types : [type]) {
    if (!member.isStringLiteral()) return null;
    out.push(member.value);
  }
  return out;
}
function unwrap(node) {
  while (ts.isAsExpression(node) || ts.isParenthesizedExpression(node) || ts.isSatisfiesExpression(node) || ts.isNonNullExpression(node)) node = node.expression;
  return node;
}
// Where a name was declared, through an import.
function declarationOf(name) {
  let symbol = checker.getSymbolAtLocation(name);
  if (symbol && symbol.flags & ts.SymbolFlags.Alias) symbol = checker.getAliasedSymbol(symbol);
  return symbol?.valueDeclaration;
}
function consume(node) {
  consumed.add(node);
  node.forEachChild(consume);
}
// A template's text with every `${…}` as `{}`. A leading `${server}` (the
// address in front of the path) is dropped, and a leading `${PATH}` that is
// itself a path (a constant, a builder) is read as that path.
function templatePath(node) {
  const pieces = [];
  if (node.head.text) pieces.push(node.head.text);
  for (const span of node.templateSpans) {
    pieces.push(span.expression);
    if (span.literal.text) pieces.push(span.literal.text);
  }
  // ${server}/api/… or ${server}${PATH}…: the address goes.
  if (typeof pieces[0] !== "string" && !pathsOf(pieces[0])) pieces.shift();
  let starts = [""];
  for (const [index, piece] of pieces.entries()) {
    if (typeof piece === "string") starts = starts.map((text) => text + piece);
    else {
      const inner = index === 0 ? pathsOf(piece) : null;
      starts = inner ? starts.flatMap((text) => inner.map((p) => text + p)) : starts.map((text) => text + "{}");
    }
  }
  return starts.every(isServerPath) ? starts : null;
}
// The server paths an expression can be, or null when it cannot be read.
function pathsOf(expression) {
  const bare = unwrap(expression);
  // A constant is consumed where it is used, whichever way it resolves.
  if (ts.isIdentifier(bare)) {
    const declaration = declarationOf(bare);
    if (declaration && ts.isVariableDeclaration(declaration) && declaration.initializer) consume(declaration.initializer);
  }
  // A function's own parameter is whatever its callers pass, and each
  // caller is read where it calls; read here it would be every path its
  // type allows (the typed client's `path: P extends PathsWith<"post">`).
  if (ts.isIdentifier(bare) && declarationOf(bare) && ts.isParameter(declarationOf(bare))) return null;
  const typed = stringLiterals(checker.getTypeAtLocation(expression));
  if (typed && typed.length > 0 && typed.every(isServerPath)) {
    consume(expression);
    return typed;
  }
  if (ts.isStringLiteralLike(bare) && isServerPath(bare.text)) {
    consume(bare);
    return [bare.text];
  }
  if (ts.isTemplateExpression(bare)) {
    const found = templatePath(bare);
    if (found) consume(bare);
    return found;
  }
  // route(P, params, query): the app's path builder (lib/api/typed.ts).
  if (ts.isCallExpression(bare) && ts.isIdentifier(bare.expression) && bare.expression.text === "route" && bare.arguments[0]) return pathsOf(bare.arguments[0]);
  // A path builder of the app's own (`notebookPath(id)`,
  // `sourcesApi.togglePath(id, …)`): an arrow function whose body is a path.
  if (ts.isCallExpression(bare) && (ts.isIdentifier(bare.expression) || ts.isPropertyAccessExpression(bare.expression))) {
    const name = ts.isPropertyAccessExpression(bare.expression) ? bare.expression.name : bare.expression;
    const declaration = declarationOf(name);
    const builder = declaration && (ts.isVariableDeclaration(declaration) || ts.isPropertyAssignment(declaration)) ? declaration.initializer : null;
    if (builder && ts.isArrowFunction(builder) && !ts.isBlock(builder.body)) return pathsOf(builder.body);
  }
  if (ts.isConditionalExpression(bare)) {
    const one = pathsOf(bare.whenTrue);
    const other = pathsOf(bare.whenFalse);
    return one && other ? [...one, ...other] : null;
  }
  if (ts.isIdentifier(bare)) {
    const declaration = declarationOf(bare);
    if (declaration && ts.isVariableDeclaration(declaration) && declaration.initializer) return pathsOf(declaration.initializer);
  }
  return null;
}
function methodsOf(expression) {
  const literals = stringLiterals(checker.getTypeAtLocation(expression));
  return literals && literals.length > 0 && literals.every((m) => METHODS.includes(m.toUpperCase())) ? literals.map((m) => m.toUpperCase()) : null;
}
// The `method` of a fetch-style options object; none is a GET.
function methodOfOptions(options) {
  if (!options) return ["GET"];
  const bare = unwrap(options);
  if (!ts.isObjectLiteralExpression(bare)) return null;
  for (const property of bare.properties) {
    if (ts.isPropertyAssignment(property) && property.name.getText() === "method") return methodsOf(property.initializer);
  }
  return ["GET"];
}
// The query a call sends: each parameter's name, with the values it can take
// when its type says (a literal union, such as the Inbox's
// `category: "status" | "announcements"`), or null for any value. These are
// what the server must go on accepting: the 2026-11-13 break was a 422 for
// category=announcements.
function queryOf(argument) {
  if (!argument) return {};
  const type = checker.getTypeAtLocation(argument);
  const out = {};
  for (const property of checker.getPropertiesOfType(type)) {
    const propertyType = checker.getTypeOfSymbolAtLocation(property, argument);
    let values = [];
    for (const member of propertyType.isUnion() ? propertyType.types : [propertyType]) {
      if (member.flags & (ts.TypeFlags.Undefined | ts.TypeFlags.Null)) continue;
      if (member.isStringLiteral() || member.isNumberLiteral()) values.push(String(member.value));
      else if (member.flags & ts.TypeFlags.BooleanLiteral) values.push(checker.typeToString(member));
      else {
        values = null;
        break;
      }
    }
    out[property.name] = values && values.length > 0 ? values.sort() : null;
  }
  return out;
}
// The query argument of a route(...) call, wherever the path came from one.
function routeQuery(expression) {
  const bare = unwrap(expression);
  if (ts.isCallExpression(bare) && ts.isIdentifier(bare.expression) && bare.expression.text === "route") return queryOf(bare.arguments[2]);
  return {};
}
function record(methods, paths, node, how, query = {}) {
  for (const method of methods) for (const p of paths) calls.push({ method, path: p, site: where(node), how, query });
}

function visit(node) {
  if (ts.isCallExpression(node) && node.arguments.length > 0) {
    const callee = node.expression;
    const name = ts.isPropertyAccessExpression(callee) ? callee.name.text : ts.isIdentifier(callee) ? callee.text : "";
    const [first, second] = node.arguments;
    if (VERBS[name] && ts.isPropertyAccessExpression(callee)) {
      // The typed client: the path is the literal P the call resolved with.
      const signature = checker.getResolvedSignature(node);
      const parameter = signature?.parameters[0] && checker.getTypeOfSymbolAtLocation(signature.parameters[0], node);
      const literal = parameter && stringLiterals(parameter);
      const paths = pathsOf(first);
      // The typed client's query is its third argument for a read or a
      // delete and its fourth for a write with a body (lib/api/typed.ts).
      const query = queryOf(node.arguments[["GET", "DELETE"].includes(VERBS[name]) ? 2 : 3]);
      if (literal && literal.length > 0 && literal.every(isServerPath)) record([VERBS[name]], literal, node, "typed", query);
      else if (paths) record([VERBS[name]], paths, node, "verb", query);
    } else if (name === "request" || /fetch/i.test(name)) {
      // api.request(path, {method}) and fetch(address, {method}).
      const paths = pathsOf(first);
      const methods = methodOfOptions(second);
      if (paths && methods) record(methods, paths, node, name === "request" ? "request" : "fetch", routeQuery(first));
    } else if (name === "postJson") {
      const paths = pathsOf(first);
      if (paths) record(["POST"], paths, node, "postJson");
    } else if (second && methodsOf(first)) {
      // The outbox's send(method, path, …) and sendLater(method, path, …).
      const paths = pathsOf(second);
      if (paths) record(methodsOf(first), paths, node, name, routeQuery(second));
    }
  }
  if ((ts.isStringLiteralLike(node) && isServerPath(node.text)) || (ts.isTemplateExpression(node) && templatePath(node))) mentions.push(node);
  ts.forEachChild(node, visit);
}
for (const file of program.getSourceFiles()) {
  if (file.fileName.startsWith(srcDir + path.sep) && !file.isDeclarationFile) visit(file);
}

// A resolution names its place as file:line, as the source read when it was
// written. The same source at a later commit has moved lines, so a
// resolution also matches the one place in that file with the same text:
// a snapshot of the next update reuses the last one's resolutions, and only
// a mention that is new, or no longer unique in its file, needs a person.
const fileOf = (site) => site.slice(0, site.lastIndexOf(":"));
function sameSite(resolution, site, text, candidates) {
  if (resolution.text !== text || fileOf(resolution.site) !== fileOf(site)) return false;
  if (resolution.site === site) return true;
  return candidates.filter((other) => fileOf(other.site) === fileOf(site) && other.text === text).length === 1;
}

// Each mention no call consumed must be resolved by hand, once.
const unresolved = mentions.filter((node) => !consumed.has(node)).map((node) => ({ site: where(node), text: node.getText() }));
const problems = [];
const used = new Set();
for (const mention of unresolved) {
  const resolution = resolutions.find((r) => !r.drop && !r.add && sameSite(r, mention.site, mention.text, unresolved));
  if (!resolution) {
    problems.push(`unresolved: ${mention.site} ${mention.text}`);
    continue;
  }
  used.add(resolution);
  for (const call of resolution.calls ?? []) calls.push({ method: call.method, path: call.path, site: mention.site, how: "resolved", query: call.query ?? {} });
}
// A call no expression shows is added by hand at the path it uses: a
// request built as data (`saveRequest` answers {method: "PATCH", path}) and
// sent later by the outbox. It must name a mention of that path, consumed or
// not, so it goes stale with the source.
for (const resolution of resolutions.filter((r) => r.add)) {
  used.add(resolution);
  const everyMention = mentions.map((node) => ({ site: where(node), text: node.getText() }));
  const at = everyMention.find((m) => sameSite(resolution, m.site, m.text, everyMention));
  if (!at) {
    problems.push(`stale addition (matches no mention): ${resolution.site} ${resolution.text}`);
    continue;
  }
  for (const call of resolution.add) calls.push({ method: call.method, path: call.path, site: at.site, how: "added", query: call.query ?? {} });
}
// A call read wrongly is dropped by hand: two conditionals that move
// together (`send(all ? "POST" : "PATCH", all ? toggle : one)`) read as four
// calls where the app makes two.
for (const resolution of resolutions.filter((r) => r.drop)) {
  used.add(resolution);
  for (const dropped of resolution.drop) {
    const same = (c) => c.method === dropped.method && c.path.replace(/\{[^}]*\}/g, "{}") === dropped.path.replace(/\{[^}]*\}/g, "{}");
    let at = calls.findIndex((c) => c.site === resolution.site && same(c));
    // The line moved: the one such call in the same file.
    if (at < 0) {
      const inFile = calls.filter((c) => fileOf(c.site) === fileOf(resolution.site) && same(c));
      if (inFile.length === 1) at = calls.indexOf(inFile[0]);
    }
    if (at < 0) problems.push(`stale drop (matches no call): ${resolution.site} ${dropped.method} ${dropped.path}`);
    else calls.splice(at, 1);
  }
}
for (const resolution of resolutions) {
  if (!used.has(resolution)) problems.push(`stale resolution (matches no mention): ${resolution.site} ${resolution.text}`);
  if (!resolution.why) problems.push(`resolution without a why: ${resolution.site}`);
}

// Each call is named by the snapshot's own template where it has one, so the
// server test can look its schema up; otherwise as the app wrote it.
const shape = (p) => p.split("?")[0].replace(/\{[^}]*\}/g, "{}");
const templates = new Map(Object.keys(spec.paths).map((p) => [shape(p), p]));
const byOperation = new Map();
for (const call of calls) {
  const template = templates.get(shape(call.path));
  const key = `${call.method} ${template ?? shape(call.path)}`;
  const entry = byOperation.get(key) ?? { method: call.method, path: template ?? call.path.split("?")[0], in_snapshot: Boolean(template && spec.paths[template][call.method.toLowerCase()]), sites: [], query: {} };
  if (!entry.sites.includes(call.site)) entry.sites.push(call.site);
  // The union over every call site: null (any value) wins over a list.
  for (const [name, values] of Object.entries(call.query)) {
    const had = entry.query[name];
    entry.query[name] = had === null || values === null ? null : [...new Set([...(had ?? []), ...values])].sort();
  }
  byOperation.set(key, entry);
}
if (problems.length > 0) {
  console.error(problems.join("\n"));
  process.exit(1);
}
const out = [...byOperation.values()].sort((a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method));
for (const entry of out) entry.sites.sort();
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
