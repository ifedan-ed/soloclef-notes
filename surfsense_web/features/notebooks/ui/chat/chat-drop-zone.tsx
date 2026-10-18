"use client";

import type { ReactNode } from "react";
import { useDropzone } from "react-dropzone";
import { FileDropOverlay } from "../sources/file-drop-overlay";

/**
 * Files dropped on a notebook's chat become notebook sources, not message
 * attachments: a notebook chat answers only from its enabled sources, so a
 * file it could not read would be useless there. The drop opens the Add
 * sources dialog's Upload tab pre-filled (parser and vision choices first),
 * the same as a drop on the Sources panel. Dragged text is left alone.
 */
export function ChatDropZone({
	onDropFiles,
	className,
	children,
}: {
	onDropFiles: (files: File[]) => void;
	className?: string;
	children: ReactNode;
}) {
	const { getRootProps, getInputProps, isDragActive } = useDropzone({
		onDrop: (files) => {
			if (files.length > 0) onDropFiles(files);
		},
		noClick: true,
		noKeyboard: true,
	});
	return (
		<section {...getRootProps({ className: `relative outline-none ${className ?? ""}` })}>
			<input {...getInputProps()} data-testid="chat-drop-input" />
			{isDragActive ? <FileDropOverlay /> : null}
			{children}
		</section>
	);
}
