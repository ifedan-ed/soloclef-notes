import assert from "node:assert/strict";
import test from "node:test";
import { QueryClient } from "@tanstack/react-query";
import {
	artifactImageBlobQueryKey,
	artifactListQueryKey,
	artifactManifestQueryKey,
	invalidatePublishedArtifact,
} from "@/features/artifacts/api/artifact-queries";

test("artifact query keys preserve cache identity boundaries", () => {
	assert.deepEqual(artifactManifestQueryKey(11), ["artifact-manifest", 11]);
	assert.deepEqual(artifactImageBlobQueryKey(11, null), [
		"artifact-image-blob",
		11,
		null,
		undefined,
	]);
	assert.deepEqual(artifactImageBlobQueryKey(11, "share", 3), [
		"artifact-image-blob",
		11,
		"share",
		3,
	]);
	assert.deepEqual(artifactListQueryKey(), ["artifact-list"]);
	assert.deepEqual(artifactListQueryKey(null), ["artifact-list"]);
	assert.deepEqual(artifactListQueryKey(13), ["artifact-list", 13]);
});

test("publishing removes artifact blobs and invalidates the manifest and every artifact list", async () => {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	});
	const manifest = artifactManifestQueryKey(11);
	const otherManifest = artifactManifestQueryKey(12);
	const accountList = artifactListQueryKey();
	const threadList = artifactListQueryKey(13);
	const blob = artifactImageBlobQueryKey(11, null);
	const sharedBlob = artifactImageBlobQueryKey(11, "share", 3);
	const otherBlob = artifactImageBlobQueryKey(12, null);

	queryClient.setQueryData(manifest, { revision: "old" });
	queryClient.setQueryData(otherManifest, { revision: "other" });
	queryClient.setQueryData(accountList, ["account"]);
	queryClient.setQueryData(threadList, ["thread"]);
	queryClient.setQueryData(blob, "blob:private");
	queryClient.setQueryData(sharedBlob, "blob:shared");
	queryClient.setQueryData(otherBlob, "blob:other");

	await invalidatePublishedArtifact(queryClient, 11);

	assert.equal(queryClient.getQueryState(blob), undefined);
	assert.equal(queryClient.getQueryState(sharedBlob), undefined);
	assert.equal(queryClient.getQueryData(otherBlob), "blob:other");
	assert.equal(queryClient.getQueryData(manifest), undefined);
	// Another artifact's manifest is not this one's to reset.
	assert.deepEqual(queryClient.getQueryData(otherManifest), { revision: "other" });
	assert.equal(queryClient.getQueryState(accountList)?.isInvalidated, true);
	assert.equal(queryClient.getQueryState(threadList)?.isInvalidated, true);

	queryClient.clear();
});
