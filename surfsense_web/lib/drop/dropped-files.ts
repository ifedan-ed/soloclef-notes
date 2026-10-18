/**
 * Files dragged in from the desktop. A folder drop arrives as flat ``File``s
 * whose ``relativePath`` (react-dropzone) or ``webkitRelativePath`` names the
 * folder, so it is regrouped here before the upload tab shows it.
 */

/** A dropped folder, with each file's path inside it. */
export interface DroppedFolder {
	name: string;
	entries: { file: File; relativePath: string }[];
}

export interface DroppedItems {
	/** Files dropped on their own. */
	loose: File[];
	/** Top-level folders, in the order they first appear. */
	folders: DroppedFolder[];
}

/** Where the drop put a file: "/Folder/sub/a.pdf" for a folder's file, "/a.pdf" alone. */
function droppedPath(file: File): string {
	const withPath = file as File & { relativePath?: string; path?: string };
	return withPath.relativePath || withPath.path || file.webkitRelativePath || file.name;
}

/**
 * Sort a drop into loose files and whole folders, so a folder can be rebuilt in
 * the workspace with its structure instead of flattening into the root.
 */
export function splitDroppedFolders(files: File[]): DroppedItems {
	const loose: File[] = [];
	const byName = new Map<string, DroppedFolder>();
	for (const file of files) {
		const parts = droppedPath(file)
			.replace(/^\.?\/+/, "")
			.split("/")
			.filter(Boolean);
		if (parts.length < 2) {
			loose.push(file);
			continue;
		}
		const [name, ...rest] = parts;
		let folder = byName.get(name);
		if (!folder) {
			folder = { name, entries: [] };
			byName.set(name, folder);
		}
		folder.entries.push({ file, relativePath: rest.join("/") });
	}
	return { loose, folders: [...byName.values()] };
}
