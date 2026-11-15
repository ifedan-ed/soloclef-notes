/**
 * A file's size as the card shows it: whole kilobytes under a megabyte (a
 * small file is never "0.0 MB"), one decimal of megabytes above. Binary
 * units, as the server's own sentences count them.
 */
export function fileSize(bytes: number): { key: "size_kb" | "size_mb"; value: string } {
	if (bytes < 1024 * 1024) {
		return { key: "size_kb", value: String(Math.max(1, Math.ceil(bytes / 1024))) };
	}
	return { key: "size_mb", value: (bytes / (1024 * 1024)).toFixed(1) };
}
