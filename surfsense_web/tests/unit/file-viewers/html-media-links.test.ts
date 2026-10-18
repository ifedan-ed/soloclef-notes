import assert from "node:assert/strict";
import test from "node:test";
import {
	buildHtmlArtifactDocument,
	htmlArtifactContentSecurityPolicy,
} from "@/features/file-viewers/html-artifact-document";
import {
	applyMediaLinks,
	findMediaRefs,
	mediaOrigins,
	workspaceIdFromContentUrl,
} from "@/features/file-viewers/html-media-links";

const REPORT = [
	"<h1>Report</h1>",
	'<video controls preload="metadata" playsinline data-surfsense-media="12:22" title="Film"></video>',
	'<audio controls data-surfsense-media="11:21"></audio>',
	'<audio controls data-surfsense-media="11:21"></audio>',
	'<video data-surfsense-media="13:23" src="https://evil.example/x.mp4"></video>',
].join("");

test("placeholders are found once each, in order", () => {
	assert.deepEqual(findMediaRefs(REPORT), [
		{ artifactId: 12, fileId: 22 },
		{ artifactId: 11, fileId: 21 },
		{ artifactId: 13, fileId: 23 },
	]);
	assert.deepEqual(findMediaRefs("<p>No media</p>"), []);
});

test("links are written in, unknown ones and existing sources are left alone", () => {
	const links = new Map([
		["12:22", "https://app.example/api/v1/public/artifact-media/a.b"],
		["13:23", "https://app.example/api/v1/public/artifact-media/c.d"],
	]);
	const html = applyMediaLinks(REPORT, links);
	assert.match(
		html,
		/<video controls preload="metadata" playsinline data-surfsense-media="12:22" src="https:\/\/app\.example\/api\/v1\/public\/artifact-media\/a\.b" title="Film"><\/video>/
	);
	// No link issued: stays without a source, so the report shows its note.
	assert.match(html, /<audio controls data-surfsense-media="11:21"><\/audio>/);
	// A src the page already had is never replaced.
	assert.match(html, /src="https:\/\/evil\.example\/x\.mp4"/);
	assert.doesNotMatch(html, /artifact-media\/c\.d/);
});

test("link attributes cannot break out of the tag", () => {
	const html = applyMediaLinks(
		'<audio data-surfsense-media="1:2"></audio>',
		new Map([["1:2", 'x" onerror="alert(1)']])
	);
	assert.equal(
		html,
		'<audio data-surfsense-media="1:2" src="x&quot; onerror=&quot;alert(1)"></audio>'
	);
});

test("the workspace comes from the artifact's content URL", () => {
	assert.equal(workspaceIdFromContentUrl("/api/v1/workspaces/7/artifacts/9/files/3/content"), 7);
	assert.equal(workspaceIdFromContentUrl("/api/v1/documents/9"), null);
});

test("media is allowed only from the links' own origins", () => {
	const links = new Map([
		["1:2", "https://app.example/api/v1/public/artifact-media/a"],
		["3:4", "https://app.example/api/v1/public/artifact-media/b"],
		["5:6", "not a url"],
	]);
	assert.deepEqual(mediaOrigins(links), ["https://app.example"]);
	assert.doesNotMatch(htmlArtifactContentSecurityPolicy(), /media-src/);
	assert.match(
		htmlArtifactContentSecurityPolicy(["https://app.example"]),
		/media-src https:\/\/app\.example;/
	);
	const page = buildHtmlArtifactDocument("<p>x</p>", { mediaSources: ["https://app.example"] });
	assert.match(page, /connect-src 'none'/);
	assert.match(page, /media-src https:\/\/app\.example/);
});
