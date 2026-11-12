import Image from "next/image";
import { cn } from "@/lib/utils";

/**
 * A full-color brand mark served from `/public/connectors/*.svg` (the
 * connector UI's asset library), shaped as a `ComponentType<{ className?: string }>`
 * so menus that take Lucide icons can take a brand's own mark too: the Tools
 * menu's searches and the notebook research box's sources both draw these.
 */
export function brandIcon(src: string, alt: string) {
	return function BrandIcon({ className }: { className?: string }) {
		return (
			<Image
				src={src}
				alt={alt}
				width={20}
				height={20}
				className={cn("select-none object-contain pointer-events-none", className)}
				draggable={false}
			/>
		);
	};
}
