import { atom } from "jotai";
import type { GetDocumentsRequest } from "@/contracts/types/document.types";

export const globalDocumentsQueryParamsAtom = atom<GetDocumentsRequest["queryParams"]>({
	page_size: 10,
	page: 0,
});

export interface AgentCreatedDocument {
	id: number;
	title: string;
	documentType: string;
	folderId: number | null;
	createdAt: number;
}

export const agentCreatedDocumentsAtom = atom<AgentCreatedDocument[]>([]);
