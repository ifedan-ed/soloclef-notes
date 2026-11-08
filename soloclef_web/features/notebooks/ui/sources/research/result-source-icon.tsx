import { Globe, Link2 } from "lucide-react";
import { isScraperToolId, SCRAPER_TOOL_ICONS } from "@/features/scraper-tools/scraper-tools";
import { cn } from "@/lib/utils";
import {
	isAcademicSourceId,
	isWebApiSourceId,
	READ_PAGE_ID,
} from "../../../model/research-sources";
import { ACADEMIC_SOURCE_ICONS } from "./academic-icons";
import { WEB_API_SOURCE_ICONS } from "./web-api-icons";

/** The brand mark of the source a result came from; a globe for any we do not know. */
export function ResultSourceIcon({ source, className }: { source: string; className?: string }) {
	if (source === READ_PAGE_ID) {
		return <Link2 className={className} aria-hidden />;
	}
	if (isScraperToolId(source)) {
		const Icon = SCRAPER_TOOL_ICONS[source];
		return <Icon className={className} />;
	}
	if (isWebApiSourceId(source)) {
		const Icon = WEB_API_SOURCE_ICONS[source];
		return <Icon className={className} />;
	}
	if (isAcademicSourceId(source)) {
		const Icon = ACADEMIC_SOURCE_ICONS[source];
		return <Icon className={className} />;
	}
	return <Globe className={cn("text-muted-foreground", className)} aria-hidden />;
}
