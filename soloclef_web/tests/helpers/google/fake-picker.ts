import type { Page } from "@playwright/test";

/** Files the stand-in Picker offers; the ids match tests/e2e/fakes/google_server.py. */
export const PICKER_FILES = [
	{ id: "e2eDriveNotes0001", name: "Drive field notes.md", mimeType: "text/markdown" },
	{ id: "e2eDrivePlan00002", name: "Research plan.txt", mimeType: "text/plain" },
	{ id: "e2eDriveFolder004", name: "A folder", mimeType: "application/vnd.google-apps.folder" },
];

/**
 * Google's Picker, faked at the browser boundary: the page asks for
 * apis.google.com/js/api.js and gets this instead. It renders a plain
 * dialog with the files above and records how the Picker was built
 * (window.__pickerBuilt) so a test can check the app id, key and token.
 */
const FAKE_PICKER_SCRIPT = `
(() => {
	const FILES = ${JSON.stringify(PICKER_FILES)};
	const Action = { PICKED: "picked", CANCEL: "cancel", ERROR: "error", LOADED: "loaded" };
	const Response = { ACTION: "action", DOCUMENTS: "docs" };
	const Document = { ID: "id", NAME: "name", MIME_TYPE: "mimeType" };
	class DocsView {
		constructor(viewId) { this.viewId = viewId; this.opts = {}; }
		setIncludeFolders(v) { this.opts.includeFolders = v; return this; }
		setSelectFolderEnabled(v) { this.opts.selectFolders = v; return this; }
		setMode(v) { this.opts.mode = v; return this; }
	}
	class PickerBuilder {
		constructor() { this.cfg = { features: [], views: [] }; }
		addView(v) { this.cfg.views.push(v.opts); return this; }
		enableFeature(f) { this.cfg.features.push(f); return this; }
		setMaxItems(n) { this.cfg.maxItems = n; return this; }
		setOAuthToken(t) { this.cfg.token = t; return this; }
		setDeveloperKey(k) { this.cfg.apiKey = k; return this; }
		setAppId(a) { this.cfg.appId = a; return this; }
		setOrigin(o) { this.cfg.origin = o; return this; }
		setTitle(t) { this.cfg.title = t; return this; }
		setSize() { return this; }
		setCallback(cb) { this.cb = cb; return this; }
		build() {
			const cfg = this.cfg, cb = this.cb;
			window.__pickerBuilt = cfg;
			let root = null;
			return {
				setVisible(show) {
					if (!show) return;
					root = document.createElement("div");
					root.setAttribute("role", "dialog");
					root.setAttribute("aria-label", "Google Picker");
					root.style.cssText = "position:fixed;inset:10%;z-index:2147483647;background:#fff;color:#000;padding:16px;border:1px solid #888";
					const title = document.createElement("h2");
					title.textContent = cfg.title;
					root.appendChild(title);
					for (const f of FILES) {
						const label = document.createElement("label");
						label.style.display = "block";
						const box = document.createElement("input");
						box.type = "checkbox";
						box.value = f.id;
						label.appendChild(box);
						label.appendChild(document.createTextNode(" " + f.name));
						root.appendChild(label);
					}
					const select = document.createElement("button");
					select.textContent = "Select";
					select.onclick = () => {
						const picked = [...root.querySelectorAll("input:checked")].map((b) => {
							const f = FILES.find((x) => x.id === b.value);
							return { id: f.id, name: f.name, mimeType: f.mimeType };
						});
						cb({ action: Action.PICKED, docs: picked });
					};
					const cancel = document.createElement("button");
					cancel.textContent = "Cancel";
					cancel.onclick = () => cb({ action: Action.CANCEL });
					root.appendChild(select);
					root.appendChild(cancel);
					document.body.appendChild(root);
				},
				dispose() { root?.remove(); root = null; },
			};
		}
	}
	window.google = window.google || {};
	window.google.picker = {
		DocsView, PickerBuilder, Action, Response, Document,
		ViewId: { DOCS: "all" },
		DocsViewMode: { LIST: "list", GRID: "grid" },
		Feature: { MULTISELECT_ENABLED: "multiselect", SUPPORT_DRIVES: "drives" },
	};
	window.gapi = { load: (_name, opts) => setTimeout(() => (opts.callback ? opts.callback() : opts()), 0) };
})();
`;

export async function installFakeGooglePicker(page: Page): Promise<void> {
	await page.route("https://apis.google.com/js/api.js", (route) =>
		route.fulfill({ status: 200, contentType: "text/javascript", body: FAKE_PICKER_SCRIPT })
	);
}
