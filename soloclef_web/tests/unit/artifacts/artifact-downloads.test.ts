import assert from "node:assert/strict";
import test from "node:test";
import {
	artifactDownloads,
	selectSvgPreviewFile,
} from "@/features/artifacts/lib/artifact-selectors";
import type { ArtifactFile, ArtifactManifest } from "@/features/artifacts/model/artifact";

// Run with: pnpm exec tsx --test tests/unit/artifacts/artifact-downloads.test.ts

function file(overrides: Partial<ArtifactFile>): ArtifactFile {
	return {
		file_id: 1,
		role: "primary",
		filename: "Asthma in children.png",
		mime_type: "image/png",
		size_bytes: 1000,
		content_url: "/api/v1/artifacts/7/files/1/content",
		...overrides,
	};
}

function manifest(files: ArtifactFile[], format = "infographic"): ArtifactManifest {
	return {
		artifact_id: 7,
		document_id: 3,
		title: "Asthma in children",
		format,
		generation: 1,
		markdown_representation: "# Asthma in children",
		files,
		updated_at: null,
	};
}

const png = file({});
const svg = file({
	file_id: 2,
	role: "preview",
	filename: "Asthma in children.svg",
	mime_type: "image/svg+xml",
	content_url: "/api/v1/artifacts/7/files/2/content",
});

test("an infographic with its poster's SVG shows the SVG; an older one has only the PNG", () => {
	assert.equal(selectSvgPreviewFile([png, svg]), svg);
	assert.equal(selectSvgPreviewFile([png]), undefined);
	// The main file is never taken for the preview, even when it is an SVG.
	assert.equal(selectSvgPreviewFile([file({ mime_type: "image/svg+xml" })]), undefined);
});

test("an infographic downloads as PNG and as SVG; the PNG from the given address", () => {
	assert.deepEqual(artifactDownloads(manifest([png, svg]), 7, "/api/v1/artifacts/7/download"), [
		{ label: "PNG", path: "/api/v1/artifacts/7/download", filename: "Asthma in children.png" },
		{ label: "SVG", path: svg.content_url, filename: "Asthma in children.svg" },
	]);
	assert.deepEqual(
		artifactDownloads(manifest([png]), 7, "/api/v1/artifacts/7/download").map((d) => d.label),
		["PNG"]
	);
});

test("a deck's PDF preview is a viewing aid, not a download", () => {
	const deck = manifest(
		[
			file({
				filename: "Deck.pptx",
				mime_type: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			}),
			file({ file_id: 2, role: "preview", filename: "Deck.pdf", mime_type: "application/pdf" }),
		],
		"pptx"
	);
	assert.deepEqual(
		artifactDownloads(deck, 7, "/api/v1/artifacts/7/download").map((d) => d.label),
		["PPTX"]
	);
});
