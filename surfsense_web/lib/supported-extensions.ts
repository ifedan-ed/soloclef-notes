const audioFileTypes: Record<string, string[]> = {
	"audio/mpeg": [".mp3", ".mpeg", ".mpga"],
	"audio/mp4": [".mp4", ".m4a"],
	"audio/wav": [".wav"],
	"audio/webm": [".webm"],
	"text/markdown": [".md", ".markdown"],
	"text/plain": [".txt"],
};

const commonTypes: Record<string, string[]> = {
	"application/pdf": [".pdf"],
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": [".docx"],
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": [".xlsx"],
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": [".pptx"],
	"text/html": [".html", ".htm"],
	"text/csv": [".csv"],
	"text/tab-separated-values": [".tsv"],
	"image/jpeg": [".jpg", ".jpeg"],
	"image/png": [".png"],
	"image/bmp": [".bmp"],
	"image/webp": [".webp"],
	"image/tiff": [".tiff"],
};

export const FILE_TYPE_CONFIG: Record<string, Record<string, string[]>> = {
	LLAMACLOUD: {
		...commonTypes,
		"application/msword": [".doc"],
		"application/vnd.ms-word.document.macroEnabled.12": [".docm"],
		"application/msword-template": [".dot"],
		"application/vnd.ms-word.template.macroEnabled.12": [".dotm"],
		"application/vnd.ms-powerpoint": [".ppt"],
		"application/vnd.ms-powerpoint.template.macroEnabled.12": [".pptm"],
		"application/vnd.ms-powerpoint.template": [".pot"],
		"application/vnd.openxmlformats-officedocument.presentationml.template": [".potx"],
		"application/vnd.ms-excel": [".xls"],
		"application/vnd.ms-excel.sheet.macroEnabled.12": [".xlsm"],
		"application/vnd.ms-excel.sheet.binary.macroEnabled.12": [".xlsb"],
		"application/vnd.ms-excel.workspace": [".xlw"],
		"application/rtf": [".rtf"],
		"application/xml": [".xml"],
		"application/epub+zip": [".epub"],
		"image/gif": [".gif"],
		"image/svg+xml": [".svg"],
		...audioFileTypes,
	},
	// Mirrors PYMUPDF4LLM_DOCUMENT_EXTENSIONS in the backend
	// (app/utils/file_extensions.py). MuPDF refuses .webp and renders
	// .xlsx with every string cell missing, so neither is offered --
	// commonTypes carries both, hence the explicit deletions below.
	PYMUPDF4LLM: {
		...commonTypes,
		"text/asciidoc": [".adoc", ".asciidoc"],
		"text/html": [".html", ".htm", ".xhtml"],
		"image/tiff": [".tiff", ".tif"],
		"image/gif": [".gif"],
		"image/svg+xml": [".svg"],
		"application/epub+zip": [".epub"],
		...audioFileTypes,
	},
	// Mirrors DATALAB_DOCUMENT_EXTENSIONS in the backend. With a LlamaCloud
	// key beside it the backend's Cloud also opens LlamaCloud's types; this
	// fallback offers Datalab's own, so it never offers more than Cloud reads.
	DATALAB: {
		...commonTypes,
		"application/msword": [".doc"],
		"application/vnd.oasis.opendocument.text": [".odt"],
		"application/vnd.ms-powerpoint": [".ppt"],
		"application/vnd.oasis.opendocument.presentation": [".odp"],
		"application/vnd.ms-excel": [".xls"],
		"application/vnd.ms-excel.sheet.macroEnabled.12": [".xlsm"],
		"application/vnd.openxmlformats-officedocument.spreadsheetml.template": [".xltx"],
		"application/vnd.oasis.opendocument.spreadsheet": [".ods"],
		"application/epub+zip": [".epub"],
		"image/gif": [".gif"],
		...audioFileTypes,
	},
	AZURE_DI: {
		...commonTypes,
		"image/heic": [".heic"],
		...audioFileTypes,
	},
	default: {
		...commonTypes,
		"application/msword": [".doc"],
		"message/rfc822": [".eml"],
		"application/epub+zip": [".epub"],
		"image/heic": [".heic"],
		"application/vnd.ms-outlook": [".msg"],
		"application/vnd.oasis.opendocument.text": [".odt"],
		"text/x-org": [".org"],
		"application/pkcs7-signature": [".p7s"],
		"application/vnd.ms-powerpoint": [".ppt"],
		"text/x-rst": [".rst"],
		"application/rtf": [".rtf"],
		"application/vnd.ms-excel": [".xls"],
		"application/xml": [".xml"],
		...audioFileTypes,
	},
};

// MuPDF opens neither of these usefully, but both ride in on
// commonTypes, so strip them from the pymupdf4llm offer rather than
// duplicating the whole common map.
for (const mime of [
	"image/webp",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
]) {
	delete FILE_TYPE_CONFIG.PYMUPDF4LLM[mime];
}
// Datalab does not list BMP; commonTypes carries it.
delete FILE_TYPE_CONFIG.DATALAB["image/bmp"];

export function getAcceptedFileTypes(etlService?: string): Record<string, string[]> {
	return FILE_TYPE_CONFIG[etlService || "default"] || FILE_TYPE_CONFIG.default;
}

export function getSupportedExtensions(
	acceptedFileTypes?: Record<string, string[]>,
	etlService?: string
): string[] {
	const types = acceptedFileTypes ?? getAcceptedFileTypes(etlService);
	return Array.from(new Set(Object.values(types).flat())).sort();
}

export function getSupportedExtensionsSet(
	acceptedFileTypes?: Record<string, string[]>,
	etlService?: string
): Set<string> {
	return new Set(
		getSupportedExtensions(acceptedFileTypes, etlService).map((ext) => ext.toLowerCase())
	);
}
