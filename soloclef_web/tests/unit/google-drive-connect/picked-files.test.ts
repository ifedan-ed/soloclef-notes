import assert from "node:assert/strict";
import test from "node:test";
import {
	DRIVE_FOLDER_MIME,
	driveImportRequest,
	MAX_PICKED_FILES,
	pickedFiles,
	pickProblem,
	returnPathFrom,
} from "@/features/google-drive-connect/lib/picked-files";

test("a Picker selection keeps files, drops folders and repeats", () => {
	const files = pickedFiles([
		{ id: "a1", name: "notes.md", mimeType: "text/markdown" },
		{ id: "f1", name: "Folder", mimeType: DRIVE_FOLDER_MIME },
		{ id: "a1", name: "notes.md", mimeType: "text/markdown" },
		{ id: "g1", name: "  ", mimeType: "application/vnd.google-apps.document" },
		{ name: "no id" },
	]);
	assert.deepEqual(files, [
		{ id: "a1", name: "notes.md", mime_type: "text/markdown" },
		{ id: "g1", name: "Untitled", mime_type: "application/vnd.google-apps.document" },
	]);
});

test("the return path stays in the app and drops a previous outcome", () => {
	assert.equal(
		returnPathFrom({
			pathname: "/dashboard/3/notebooks/7",
			search: "?google_drive=connected&tab=x",
		}),
		"/dashboard/3/notebooks/7?tab=x"
	);
	assert.equal(returnPathFrom({ pathname: "/dashboard/3", search: "" }), "/dashboard/3");
});

const file = (n: number) => ({
	id: `driveFile${String(n).padStart(4, "0")}`,
	name: `f${n}.md`,
	mime_type: "text/markdown",
});

test("the import request names the notebook and nothing that picks an owner", () => {
	// The picks land in the signed-in account's library: the server takes the
	// account from the session, so the body carries no owner of any kind.
	const files = [file(1)];
	assert.deepEqual(driveImportRequest({ notebookId: 7, files }), { files, notebook_id: 7 });
	// No notebook is said outright (library only), not left out.
	assert.deepEqual(driveImportRequest({ notebookId: null, files }), {
		files,
		notebook_id: null,
	});
	assert.equal("workspace_id" in driveImportRequest({ notebookId: 7, files }), false);
});

test("a selection the server would refuse is caught before it is sent", () => {
	const many = (count: number) => Array.from({ length: count }, (_, n) => file(n));
	assert.equal(pickProblem([]), "none");
	assert.equal(pickProblem(many(1)), null);
	assert.equal(pickProblem(many(MAX_PICKED_FILES)), null);
	assert.equal(pickProblem(many(MAX_PICKED_FILES + 1)), "too_many");
});

test("only folders picked leaves nothing to send", () => {
	assert.equal(
		pickProblem(pickedFiles([{ id: "f1", name: "Folder", mimeType: DRIVE_FOLDER_MIME }])),
		"none"
	);
});
