import assert from "node:assert/strict";
import test from "node:test";
import { artifactDownloadPath } from "@/features/artifacts/api/artifact-download-path";

// The account is the scope: the server takes the owner from the session, so
// the path names the artifact and nothing else.
test("an artifact downloads from its own id, with no workspace in the path", () => {
	assert.equal(artifactDownloadPath(42), "/api/v1/artifacts/42/download");
});
