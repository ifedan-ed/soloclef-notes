export interface ViewableFile {
	filename: string;
	mime_type: string;
	size_bytes: number;
	content_url: string;
	role?: string;
}

export interface FileViewerProps {
	primary: ViewableFile;
	files: readonly ViewableFile[];
	zoomControlsContainer?: HTMLElement | null;
	/** 1-based page to open at, for paged formats (PDF). */
	initialPage?: number;
	/** Text to find and mark on the initial page, for paged formats (PDF). */
	highlightText?: string;
}
