import assert from "node:assert/strict";
import test from "node:test";
import { splitDroppedFolders } from "@/lib/drop/dropped-files";

function dropped(path: string, size = 10): File {
	const file = new File([new Uint8Array(size)], path.split("/").pop() ?? path);
	Object.defineProperty(file, "relativePath", { value: path });
	return file;
}

test("a dropped folder keeps its structure; loose files stay loose", () => {
	const { loose, folders } = splitDroppedFolders([
		dropped("/notes.pdf"),
		dropped("/Trial/protocol.pdf"),
		dropped("/Trial/data/sheet.csv"),
		dropped("/Trial/data/raw/a.pdf"),
		dropped("/Other/x.pdf"),
		dropped("./plain.docx"),
	]);
	assert.deepEqual(
		loose.map((f) => f.name),
		["notes.pdf", "plain.docx"]
	);
	assert.deepEqual(
		folders.map((f) => [f.name, f.entries.map((e) => e.relativePath)]),
		[
			["Trial", ["protocol.pdf", "data/sheet.csv", "data/raw/a.pdf"]],
			["Other", ["x.pdf"]],
		]
	);
});

test("a plain file with no path is loose", () => {
	const { loose, folders } = splitDroppedFolders([new File(["x"], "slides.pptx")]);
	assert.deepEqual(
		loose.map((f) => f.name),
		["slides.pptx"]
	);
	assert.deepEqual(folders, []);
});
