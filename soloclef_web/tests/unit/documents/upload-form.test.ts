import assert from "node:assert/strict";
import test from "node:test";

import { uploadFormData } from "@/lib/apis/upload-form";

const file = (name: string) => new File(["x"], name, { type: "text/plain" });

test("the text fields come before every file part", () => {
	// The server streams the body: it must know the parser before the first
	// file byte. No field says whose upload it is; that is the session's.
	const form = uploadFormData({
		files: [file("a.pdf"), file("b.pdf")],
		parser: "llamaparse",
		useVisionLlm: false,
	});
	assert.deepEqual([...form.keys()], ["parser", "use_vision_llm", "files", "files"]);
});

test("the fields carry the choice exactly as made", () => {
	const form = uploadFormData({
		files: [file("a.pdf")],
		parser: "unstructured",
		useVisionLlm: true,
	});
	assert.equal(form.has("workspace_id"), false);
	assert.equal(form.get("parser"), "unstructured");
	assert.equal(form.get("use_vision_llm"), "true");
	assert.equal((form.getAll("files")[0] as File).name, "a.pdf");
});

test("the folder upload also sends its fields ahead of the files", async () => {
	// Read from the source: the folder builder lives inside a method that
	// posts, and the order of its appends is the whole of what matters.
	const { readFileSync } = await import("node:fs");
	const source = readFileSync(
		new URL("../../../lib/apis/documents-api.service.ts", import.meta.url),
		"utf8"
	);
	const folder = source.slice(source.indexOf("folderUploadFiles = async"));
	assert.equal(folder.includes("workspace_id"), false, "no upload names a workspace");
	assert.ok(folder.indexOf('formData.append("folder_name"') > 0);
	assert.ok(
		folder.indexOf('formData.append("folder_name"') < folder.indexOf('formData.append("files"'),
		"folder_name must be appended before files"
	);
	assert.ok(folder.indexOf('formData.append("parser"') < folder.indexOf('formData.append("files"'));
});
