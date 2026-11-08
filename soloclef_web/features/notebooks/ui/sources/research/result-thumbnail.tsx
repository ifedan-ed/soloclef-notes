"use client";

import { useState } from "react";
import { buildBackendUrl } from "@/lib/env-config";
import { cn } from "@/lib/utils";
import type { ResearchResult } from "../../../model/research";
import { ResultSourceIcon } from "./result-source-icon";

/**
 * A fixed-size tile for a result: the source's mark sits in it from the first
 * paint, and the image (or the site's favicon) fades in over it once loaded,
 * so the list never shifts. Images come through our own proxy, lazily, and
 * without a referrer; one that fails simply leaves the mark showing.
 */
export function ResultThumbnail({ result }: { result: ResearchResult }) {
	const [loaded, setLoaded] = useState(false);
	const [failed, setFailed] = useState(false);
	const image = result.thumbnail_url ?? result.favicon_url ?? null;
	const isFavicon = !result.thumbnail_url && Boolean(result.favicon_url);
	const showImage = Boolean(image) && !failed;

	return (
		<span
			className="relative flex size-12 shrink-0 items-center justify-center overflow-hidden rounded-md bg-muted"
			aria-hidden
		>
			<ResultSourceIcon
				source={result.source}
				className={cn("size-5 transition-opacity", showImage && loaded && "opacity-0")}
			/>
			{showImage && image ? (
				// biome-ignore lint/performance/noImgElement: proxied remote images of unknown size; next/image would need every CDN allow-listed.
				<img
					src={buildBackendUrl(image)}
					alt=""
					width={isFavicon ? 20 : 48}
					height={isFavicon ? 20 : 48}
					loading="lazy"
					decoding="async"
					referrerPolicy="no-referrer"
					draggable={false}
					onLoad={() => setLoaded(true)}
					onError={() => setFailed(true)}
					className={cn(
						"absolute opacity-0 transition-opacity duration-150",
						isFavicon ? "size-5 object-contain" : "inset-0 size-full object-cover",
						loaded && "opacity-100"
					)}
				/>
			) : null}
		</span>
	);
}
