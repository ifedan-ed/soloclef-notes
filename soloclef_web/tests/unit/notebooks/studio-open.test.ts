import assert from "node:assert/strict";
import test from "node:test";
import { resolveArtifactRenderer } from "@/features/artifacts/lib/artifact-resolution";
import { MIME_VIEWER_TYPES } from "@/features/artifacts/rendering/mime-viewer-registry";
import Mp4FileViewer from "@/features/file-viewers/mp4-file-viewer";
import { studioOpenAction } from "@/features/notebooks/lib/open-output";
import type { StudioOutput } from "@/features/notebooks/model/notebook";
import { buildBackendUrl } from "@/lib/env-config";

function output(overrides: Partial<StudioOutput>): StudioOutput {
	return {
		id: 1,
		kind: "video",
		title: "Video Overview",
		status: "ready",
		artifact_id: 8,
		artifact_format: "video",
		duration_seconds: 487,
		source_count: 1,
		options: {},
		created_at: "2026-09-23T00:00:00Z",
		...overrides,
	};
}

test("a ready video opens the studio output view, audio the studio player", () => {
	assert.deepEqual(studioOpenAction(output({})), {
		type: "studio-view",
		outputId: 1,
		artifactId: 8,
	});
	assert.deepEqual(studioOpenAction(output({ kind: "audio", artifact_id: 5 })), {
		type: "audio-player",
		artifactId: 5,
	});
	assert.equal(studioOpenAction(output({ status: "running" })), null);
	assert.equal(studioOpenAction(output({ artifact_id: null })), null);
});

test("the viewer resolves a video artifact to the MP4 player on its content URL", () => {
	const resolution = resolveArtifactRenderer({
		format: "video",
		primaryMimeType: "video/mp4",
		hasPrimary: true,
		hasMarkdown: true,
		// The semantic renderers (their registry pulls in the Markdown stack).
		semanticFormats: new Set(["mindmap", "flashcards", "quiz"]),
		mimeTypes: MIME_VIEWER_TYPES,
	});
	assert.deepEqual(resolution, { kind: "mime", key: "video/mp4" });

	const contentUrl = "/api/v1/workspaces/1/artifacts/8/files/14/content";
	const primary = {
		filename: "video.mp4",
		mime_type: "video/mp4",
		size_bytes: 7317621,
		content_url: contentUrl,
	};
	const frame = Mp4FileViewer({ primary, files: [primary] });
	const player = frame.props.children;
	assert.equal(player.props.src, buildBackendUrl(contentUrl));
});
