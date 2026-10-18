import assert from "node:assert/strict";
import test from "node:test";
import {
	DRIVE_FOLDER_MIME,
	pickedFiles,
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
