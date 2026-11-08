import assert from "node:assert/strict";
import test from "node:test";

import type { PublicSharedOutput } from "@/contracts/types/public-chat.types";
import { getPublicChatResponse } from "@/contracts/types/public-chat.types";
import { resolveArtifactRenderer } from "@/features/artifacts/lib/artifact-resolution";
import { selectPrimaryArtifactFile } from "@/features/artifacts/lib/artifact-selectors";
import { ArtifactManifestSchema } from "@/features/artifacts/model/artifact";
import {
	publicLinkMetadata,
	summarizePublicLink,
} from "@/features/public-sharing/lib/public-link-metadata";
import {
	sharedOutputManifest,
	sharedOutputViewer,
} from "@/features/public-sharing/model/shared-output";

// Run with: pnpm exec tsx --test tests/unit/public-sharing/shared-output.test.ts

const PPTX = "application/vnd.openxmlformats-officedocument.presentationml.presentation";

const deck: PublicSharedOutput = {
	title: "Eight planets fact sheet",
	kind: "slides",
	format: "pptx",
	markdown: "# Eight planets\n\nSlide text",
	artifact_id: 114,
	file: { filename: "deck.pptx", mime_type: PPTX, size_bytes: 9, display: "file" },
	generation: 2,
	files: [
		{
			file_id: 21,
			role: "primary",
			filename: "deck.pptx",
			mime_type: PPTX,
			size_bytes: 9,
			content_url: "/api/v1/public/tok/artifacts/114/files/21/content",
		},
		{
			file_id: 20,
			role: "preview",
			filename: "deck.pdf",
			mime_type: "application/pdf",
			size_bytes: 7,
			content_url: "/api/v1/public/tok/artifacts/114/files/20/content",
		},
	],
};

test("a shared output is the owner's manifest, so the owner's viewer draws it", () => {
	const manifest = sharedOutputManifest("tok", deck);

	assert.equal(ArtifactManifestSchema.safeParse(manifest).success, true);
	assert.equal(manifest.markdown_representation, deck.markdown);
	assert.equal(manifest.generation, 2);
	// The deck's PDF preview, which the owner's slide viewer shows, comes too.
	assert.deepEqual(
		manifest.files.map((file) => file.role),
		["primary", "preview"]
	);
	assert.equal(selectPrimaryArtifactFile(manifest)?.filename, "deck.pptx");
});

test("each kind opens where the owner's Studio opens it", () => {
	assert.equal(sharedOutputViewer({ kind: "report" }), "report");
	assert.equal(sharedOutputViewer({ kind: "audio" }), "audio");
	for (const kind of ["mindmap", "flashcards", "quiz", "slides", "data_table", "infographic"]) {
		assert.equal(sharedOutputViewer({ kind }), "artifact");
	}
	// Flashcards, a quiz and a mind map get their own viewers, never a download.
	const [primary] = deck.files ?? [];
	for (const format of ["flashcards", "quiz", "mindmap"]) {
		const manifest = sharedOutputManifest("tok", {
			...deck,
			format,
			files: [{ ...primary, mime_type: "application/json", filename: "x.json" }],
		});
		const resolution = resolveArtifactRenderer({
			format: manifest.format,
			primaryMimeType: selectPrimaryArtifactFile(manifest)?.mime_type,
			hasPrimary: true,
			hasMarkdown: true,
			semanticFormats: new Set(["mindmap", "flashcards", "quiz"]),
			mimeTypes: new Set(),
		});
		assert.deepEqual(resolution, { kind: "semantic", key: format });
	}
});

test("a server without the file list still shows its main file", () => {
	const manifest = sharedOutputManifest("tok", {
		...deck,
		files: undefined,
		generation: undefined,
	});

	assert.deepEqual(manifest.files, [
		{
			file_id: 0,
			role: "primary",
			filename: "deck.pptx",
			mime_type: PPTX,
			size_bytes: 9,
			content_url: "/api/v1/public/tok/artifacts/114/content",
		},
	]);
	assert.equal(manifest.generation, 1);
	assert.deepEqual(sharedOutputManifest("tok", { ...deck, file: null, files: [] }).files, []);
});

test("the page's answer carries the files and still parses without them", () => {
	const page = {
		kind: "output",
		thread: { title: deck.title, created_at: "2026-10-11T00:00:00Z" },
		messages: [],
		output: deck,
		expires_at: null,
	};
	assert.equal(getPublicChatResponse.parse(page).output?.files?.length, 2);
	const { files: _files, generation: _generation, ...older } = deck;
	assert.equal(getPublicChatResponse.safeParse({ ...page, output: older }).success, true);
});

test("a link's preview names what was shared, not the site", () => {
	const summary = summarizePublicLink({
		kind: "output",
		thread: { title: "Planets flashcards" },
		output: { title: "Planets flashcards", kind: "flashcards" },
	});
	const meta = publicLinkMetadata(summary);

	assert.equal(meta.title, "Planets flashcards");
	assert.match(String(meta.description), /^Flashcards · shared read-only from /);
	assert.equal((meta.openGraph as { title?: string }).title, "Planets flashcards");
	assert.deepEqual(meta.robots, { index: false, follow: false, nocache: true });
	assert.equal(meta.referrer, "no-referrer");

	const chat = publicLinkMetadata(
		summarizePublicLink({ kind: "chat", thread: { title: "Ask about Mars" } })
	);
	assert.equal(chat.title, "Ask about Mars");
	assert.match(String(chat.description), /^Chat · shared read-only/);

	// A link that is gone names nothing, and is still never indexed.
	const gone = publicLinkMetadata(summarizePublicLink({ detail: "Not found" }));
	assert.equal(gone.openGraph, undefined);
	assert.deepEqual(gone.robots, { index: false, follow: false, nocache: true });
});
