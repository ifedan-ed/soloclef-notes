import type { EtlCapabilities } from "@/contracts/types/document.types";

/**
 * Capabilities as the server answers them, for the parser tests: all four
 * parsers in its order, Datalab and Unstructured with no key. Tests change
 * one thing at a time through `over`.
 */
export function capabilities(over: Partial<EtlCapabilities> = {}): EtlCapabilities {
	return {
		default_parser: "local",
		parsers: [
			{
				parser: "local",
				available: true,
				unavailable_reason: null,
				document_extensions: [".pdf", ".docx"],
			},
			{
				parser: "datalab",
				available: false,
				unavailable_reason: "not_configured",
				document_extensions: [".pdf", ".docx", ".png"],
			},
			{
				parser: "llamaparse",
				available: true,
				unavailable_reason: null,
				document_extensions: [".pdf", ".docx", ".XLSX"],
			},
			{
				parser: "unstructured",
				available: false,
				unavailable_reason: "not_configured",
				document_extensions: [".pdf", ".epub"],
			},
		],
		always_handled: { plaintext: [".md", ".txt"], direct_convert: [".csv", ".html"] },
		vision_available: false,
		picture_extensions: [".png", ".jpg", ".heic"],
		max_file_size_bytes: 50 * 1024 * 1024,
		max_files_per_upload: 20,
		...over,
	};
}
