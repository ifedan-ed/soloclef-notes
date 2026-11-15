"use client";

import { Download } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { downloadArtifactFile } from "@/features/artifacts/api/artifact-download";
import type { ArtifactDownload } from "@/features/artifacts/lib/artifact-selectors";
import { ArtifactDownloadButton } from "./artifact-download-button";

/**
 * A viewer header's download: one button for an artifact with one file, a
 * menu of its formats (an infographic's PNG and SVG) when it has more.
 */
export function ArtifactDownloads({
	downloads,
	className,
}: {
	/** From artifactDownloads(); the first is the artifact's main file. */
	downloads: readonly ArtifactDownload[];
	className?: string;
}) {
	const [downloading, setDownloading] = useState(false);
	const [first] = downloads;
	if (!first) return null;
	if (downloads.length === 1) {
		return (
			<ArtifactDownloadButton path={first.path} filename={first.filename} className={className} />
		);
	}

	const download = async (file: ArtifactDownload) => {
		setDownloading(true);
		try {
			await downloadArtifactFile(file.path, file.filename);
		} finally {
			setDownloading(false);
		}
	};

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button
					type="button"
					variant="ghost"
					size="icon"
					className={className}
					disabled={downloading}
				>
					<Download className="size-4" />
					<span className="sr-only">Download</span>
				</Button>
			</DropdownMenuTrigger>
			{/* Above the phone's artifact drawer (z-80), which this header sits in. */}
			<DropdownMenuContent align="end" className="z-90 min-w-24">
				{downloads.map((file) => (
					<DropdownMenuItem key={file.path} onSelect={() => void download(file)}>
						{file.label}
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
