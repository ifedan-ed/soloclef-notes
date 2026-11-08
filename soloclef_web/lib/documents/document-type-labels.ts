export function getDocumentTypeLabel(type: string): string {
	const labelMap: Record<string, string> = {
		EXTENSION: "Extension",
		CRAWLED_URL: "Web Page",
		FILE: "File",
		YOUTUBE_VIDEO: "YouTube Video",
		GOOGLE_DRIVE_FILE: "Google Drive",
		LOCAL_FOLDER_FILE: "Local Folder",
		NOTE: "Note",
	};
	return (
		labelMap[type] ||
		type
			.split("_")
			.map((word) => word.charAt(0) + word.slice(1).toLowerCase())
			.join(" ")
	);
}
