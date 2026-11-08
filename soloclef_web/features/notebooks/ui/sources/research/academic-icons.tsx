import type { ComponentType } from "react";
import { brandIcon } from "@/features/scraper-tools/platform-icons";
import type { AcademicSourceId } from "../../../model/research-sources";

/** Marks for the scholarly sources, drawn in each service's colours. */
export const ACADEMIC_SOURCE_ICONS: Record<
	AcademicSourceId,
	ComponentType<{ className?: string }>
> = {
	pubmed: brandIcon("/connectors/pubmed.svg", "PubMed"),
	arxiv: brandIcon("/connectors/arxiv.svg", "arXiv"),
	medrxiv: brandIcon("/connectors/medrxiv.svg", "medRxiv"),
	google_scholar: brandIcon("/connectors/google-scholar.svg", "Google Scholar"),
};
