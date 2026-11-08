import type { PickedDriveFile, PickerSession } from "../api/google-drive-api";
import { type PickerDoc, pickedFiles } from "./picked-files";

const PICKER_SCRIPT_URL = "https://apis.google.com/js/api.js";
/** Fired on window with ``detail: boolean`` when the Picker opens or closes. */
export const PICKER_VISIBILITY_EVENT = "google-picker-visibility";

function announce(open: boolean) {
	window.dispatchEvent(new CustomEvent(PICKER_VISIBILITY_EVENT, { detail: open }));
}
export const MAX_PICKED_FILES = 50;

let pickerReady: Promise<void> | null = null;

function loadScript(): Promise<void> {
	if (typeof window !== "undefined" && window.gapi) return Promise.resolve();
	return new Promise((resolve, reject) => {
		const script = document.createElement("script");
		script.src = PICKER_SCRIPT_URL;
		script.async = true;
		script.onload = () => resolve();
		script.onerror = () => reject(new Error("Google Picker could not be loaded"));
		document.head.appendChild(script);
	});
}

/** Google's Picker library, loaded once per page. */
export function loadGooglePicker(): Promise<void> {
	if (!pickerReady) {
		pickerReady = loadScript()
			.then(
				() =>
					new Promise<void>((resolve, reject) => {
						gapi.load("picker", {
							callback: () => resolve(),
							onerror: () => reject(new Error("Google Picker could not be loaded")),
						});
					})
			)
			.catch((error) => {
				pickerReady = null;
				throw error;
			});
	}
	return pickerReady;
}

/**
 * Open the Google Picker for one person's grant; resolves with the files
 * picked, or null when they closed it. ``setAppId`` is what grants each
 * picked file to this app under the drive.file scope.
 */
export function pickDriveFiles(
	session: PickerSession,
	title: string
): Promise<PickedDriveFile[] | null> {
	return new Promise((resolve, reject) => {
		const view = new google.picker.DocsView(google.picker.ViewId.DOCS)
			.setIncludeFolders(true)
			.setSelectFolderEnabled(false)
			.setMode(google.picker.DocsViewMode.LIST);
		const builder = new google.picker.PickerBuilder()
			.addView(view)
			.enableFeature(google.picker.Feature.MULTISELECT_ENABLED)
			.enableFeature(google.picker.Feature.SUPPORT_DRIVES)
			.setMaxItems(MAX_PICKED_FILES)
			.setOAuthToken(session.access_token)
			.setDeveloperKey(session.api_key)
			.setAppId(session.app_id)
			.setOrigin(window.location.origin)
			.setTitle(title);
		if (window.innerWidth < 640) {
			builder.setSize(window.innerWidth - 32, Math.round(window.innerHeight * 0.75));
		}
		const close = () => {
			picker.dispose();
			announce(false);
		};
		const picker = builder
			.setCallback((data: google.picker.ResponseObject) => {
				const action = data[google.picker.Response.ACTION];
				if (action === google.picker.Action.PICKED) {
					const docs = (data[google.picker.Response.DOCUMENTS] ?? []).map(
						(doc): PickerDoc => ({
							id: doc[google.picker.Document.ID],
							name: doc[google.picker.Document.NAME],
							mimeType: doc[google.picker.Document.MIME_TYPE],
						})
					);
					close();
					resolve(pickedFiles(docs));
				} else if (action === google.picker.Action.CANCEL) {
					close();
					resolve(null);
				} else if (action === google.picker.Action.ERROR) {
					close();
					reject(new Error("Google Drive could not open the file picker"));
				}
			})
			.build();
		announce(true);
		picker.setVisible(true);
	});
}
