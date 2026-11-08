import assert from "node:assert/strict";
import test from "node:test";
import type { RemoteEntry } from "@/features/nextcloud-files/api/nextcloud-files-api";
import {
	breadcrumbs,
	countPicked,
	fileKind,
	formatBytes,
	isPickable,
	parentPath,
	toggleAll,
	toggleEntry,
	withoutUnreadable,
} from "@/features/nextcloud-files/lib/picker-selection";

function entry(path: string, isFolder = false, size: number | null = 10): RemoteEntry {
	return {
		path,
		name: path.split("/").pop() ?? "",
		is_folder: isFolder,
		size,
		modified: null,
		content_type: null,
		etag: "e",
	};
}

const accepted = new Set([".pdf", ".md"]);

test("breadcrumbs run from the root to the open folder, names with spaces kept", () => {
	assert.deepEqual(breadcrumbs("/Open Notebook/Week 1"), [
		{ name: "", path: "/" },
		{ name: "Open Notebook", path: "/Open Notebook" },
		{ name: "Week 1", path: "/Open Notebook/Week 1" },
	]);
	assert.deepEqual(breadcrumbs("/"), [{ name: "", path: "/" }]);
	assert.equal(parentPath("/Open Notebook/Résumé.pdf"), "/Open Notebook");
	assert.equal(parentPath("/a.pdf"), "/");
});

test("folders are always pickable; files only when the parser reads them", () => {
	assert.equal(isPickable(entry("/Folder", true), accepted), true);
	assert.equal(isPickable(entry("/notes.MD"), accepted), true);
	assert.equal(isPickable(entry("/movie.mkv"), accepted), false);
	assert.equal(isPickable(entry("/notes.md"), null), false);
});

test("ticking twice unticks, and select-all skips what cannot be read", () => {
	const once = toggleEntry(new Map(), entry("/a.pdf"));
	assert.deepEqual([...once.values()], [{ path: "/a.pdf", kind: "file" }]);
	assert.equal(toggleEntry(once, entry("/a.pdf")).size, 0);

	const listing = [entry("/F", true), entry("/a.pdf"), entry("/b.mkv")];
	const all = toggleAll(new Map(), listing, accepted);
	assert.deepEqual([...all.keys()].sort(), ["/F", "/a.pdf"]);
	assert.equal(toggleAll(all, listing, accepted).size, 0);
	assert.deepEqual(countPicked(all), { files: 1, folders: 1 });
});

test("switching parser drops picked files the new one cannot read", () => {
	const picked = toggleAll(
		new Map(),
		[entry("/F", true), entry("/a.pdf"), entry("/n.md")],
		accepted
	);
	const next = withoutUnreadable(picked, new Set([".md"]));
	assert.deepEqual([...next.keys()].sort(), ["/F", "/n.md"]);
});

test("sizes read like a file manager and icons follow the type", () => {
	assert.equal(formatBytes(null), "");
	assert.equal(formatBytes(512), "512 B");
	assert.equal(formatBytes(256815), "251 KB");
	assert.equal(formatBytes(13162616), "13 MB");
	assert.equal(formatBytes(1536), "1.5 KB");
	assert.equal(fileKind(entry("/F", true)), "folder");
	assert.equal(fileKind(entry("/deck.pptx")), "slides");
	assert.equal(fileKind(entry("/x.unknown")), "other");
});
