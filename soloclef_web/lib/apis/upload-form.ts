import type { DocumentParser } from "@/contracts/types/document.types";

/**
 * The multipart body of POST /api/v1/documents/fileupload.
 *
 * The order of the parts is part of the contract. The server streams the
 * body straight into file storage and never holds a file in memory, so it
 * has to know the parser (to refuse a file it does not read) before the
 * first file byte arrives. FormData sends its entries in the order they were
 * appended, so the two text fields are appended first, here and nowhere
 * else. Whose library the files land in is not a field: it is the signed-in
 * account's.
 */
export function uploadFormData(upload: {
	files: File[];
	parser: DocumentParser;
	useVisionLlm: boolean;
}): FormData {
	const form = new FormData();
	form.append("parser", upload.parser);
	form.append("use_vision_llm", String(upload.useVisionLlm));
	for (const file of upload.files) form.append("files", file);
	return form;
}
