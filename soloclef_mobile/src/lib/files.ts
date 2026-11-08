/**
 * Moving files between the phone and the server: uploads for sources,
 * authenticated downloads for Studio outputs, and handing a file to the share
 * sheet or another app (the phone's answer to the web's download button).
 */
import { Directory, File, Paths, UploadType } from "expo-file-system";
import * as IntentLauncher from "expo-intent-launcher";
import * as Sharing from "expo-sharing";
import { Platform } from "react-native";
import type { ApiClient } from "./api/client";
import { ApiError, parseErrorBody, SessionExpiredError } from "./api/errors";
import { type DocumentParser, type UploadResponse, uploadResponseSchema } from "./api/schemas";

function freshDirectory(parent: string): Directory {
	const dir = new Directory(Paths.cache, parent, `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`);
	dir.create({ intermediates: true, idempotent: true });
	return dir;
}

/** Filenames become document titles on the server: keep the original, minus path separators. */
export function safeFilename(name: string): string {
	const cleaned = name.replace(/[/\\:*?"<>|\u0000-\u001f]/g, "_").trim();
	return cleaned || "file";
}

/**
 * Upload one picked file to ``POST /api/v1/documents/fileupload`` exactly as
 * the web's upload dialog does (multipart ``files``, ``workspace_id``,
 * ``use_vision_llm=false``, and the Local/Cloud ``parser``). The picker's copy
 * is renamed to the original filename first, because the server titles the
 * document after it.
 */
export async function uploadDocument(
	api: ApiClient,
	picked: { uri: string; name: string; mimeType?: string | null },
	{
		workspaceId,
		parser,
		onProgress,
	}: { workspaceId: number; parser?: DocumentParser; onProgress?: (fraction: number) => void }
): Promise<UploadResponse> {
	const dir = freshDirectory("uploads");
	const file = new File(dir, safeFilename(picked.name));
	await new File(picked.uri).copy(file);
	try {
		const send = async () =>
			file.upload(api.url("/api/v1/documents/fileupload"), {
				httpMethod: "POST",
				uploadType: UploadType.MULTIPART,
				fieldName: "files",
				mimeType: picked.mimeType ?? undefined,
				parameters: {
					workspace_id: String(workspaceId),
					use_vision_llm: "false",
					...(parser ? { parser } : {}),
				},
				headers: await api.authHeaders(),
				onProgress: onProgress
					? ({ bytesSent, totalBytes }) => totalBytes > 0 && onProgress(bytesSent / totalBytes)
					: undefined,
			});
		let result = await send();
		if (result.status === 401) {
			if (!(await api.refresh())) throw new SessionExpiredError();
			result = await send();
		}
		let body: unknown = null;
		try {
			body = JSON.parse(result.body);
		} catch {
			// not JSON
		}
		if (result.status < 200 || result.status >= 300) {
			const parsed = parseErrorBody(body);
			throw new ApiError(parsed.message || `Upload failed (${result.status})`, result.status, parsed.code);
		}
		return uploadResponseSchema.parse(body);
	} finally {
		try {
			dir.delete();
		} catch {
			// cache is cleared by the OS eventually
		}
	}
}

/** Download an authenticated file into the cache; the same path is reused while it exists. */
export async function downloadToCache(
	api: ApiClient,
	path: string,
	filename: string,
	cacheKey: string,
	onProgress?: (fraction: number) => void
): Promise<File> {
	const dir = new Directory(Paths.cache, "outputs", cacheKey);
	dir.create({ intermediates: true, idempotent: true });
	const target = new File(dir, safeFilename(filename));
	if (target.exists && target.size > 0) return target;
	const download = async () =>
		File.downloadFileAsync(api.url(path), target, {
			headers: await api.authHeaders(),
			idempotent: true,
			onProgress: onProgress
				? ({ bytesWritten, totalBytes }) => totalBytes > 0 && onProgress(bytesWritten / totalBytes)
				: undefined,
		});
	try {
		return await download();
	} catch (error) {
		// authHeaders() refreshes ahead of expiry, so a 401 here means the token
		// was revoked or raced a rotation: refresh once and retry.
		if (/\b401\b/.test(String(error)) && (await api.refresh().catch(() => false))) return download();
		throw error;
	}
}

export async function shareFile(file: File, mimeType?: string): Promise<void> {
	if (!(await Sharing.isAvailableAsync())) throw new Error("Sharing is not available");
	await Sharing.shareAsync(file.uri, { mimeType, dialogTitle: file.name });
}

/**
 * Open a downloaded file in the app that handles its type (a PDF viewer,
 * Office, …). Android needs a content:// URI and an intent; iOS gets the
 * share sheet, whose "Open in…" and Quick Look cover the same need.
 */
export async function openExternally(file: File, mimeType: string): Promise<void> {
	if (Platform.OS === "android") {
		await IntentLauncher.startActivityAsync("android.intent.action.VIEW", {
			data: file.contentUri,
			type: mimeType,
			flags: 1, // FLAG_GRANT_READ_URI_PERMISSION
		});
		return;
	}
	await shareFile(file, mimeType);
}
