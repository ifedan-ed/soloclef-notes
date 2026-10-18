"""The Drive Picker page the browser sheet shows (plan item B6), as a string.

Google's Picker is Google's own JavaScript and needs a page to run on, with
this person's short-lived Drive access token (``drive.file``: it reaches only
what the person picks). The page is the web's Picker flow
(``surfsense_web/features/google-drive-connect/lib/google-picker.ts``) on its
own: it opens the Picker at once, posts the picks with the page's ticket to
``…/drive-picker/import``, and goes back to the app with how many were
added. Cancel goes back with ``error=cancelled``; anything else says what
went wrong, with Back to the app (and Try again when the Picker itself
failed: an import that failed has spent the page's ticket).

Nothing secret is in the address. The token and the ticket are in the
page's body, as JSON a script reads (escaped so no text can close the
script element); the page is never cached, never framed, tells other sites
only its origin (Google checks the Picker's API key against it; the path,
which names the spent code, never leaves), and runs only scripts carrying its nonce (``strict-dynamic`` lets Google's
loader bring the Picker's own).
"""

from __future__ import annotations

import html
import json
import secrets
from dataclasses import dataclass

#: What the Google Picker allows in one pick (the server's MAX_PICKED_FILES).
MAX_PICKED_FILES = 50
PICKER_SCRIPT_URL = "https://apis.google.com/js/api.js"


@dataclass(frozen=True)
class PickerPage:
    access_token: str
    api_key: str
    app_id: str
    ticket: str
    import_path: str
    done_url: str


def new_nonce() -> str:
    return secrets.token_urlsafe(18)


def script_json(value: object) -> str:
    """JSON safe inside a <script> element: no "<", ">", "&" or line separators left as-is."""
    text = json.dumps(value, ensure_ascii=True)
    return text.replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026")


def security_headers(nonce: str) -> dict[str, str]:
    """No caching, no framing, only the origin as referrer; only this page's own scripts (and what they load)."""
    return {
        "Cache-Control": "no-store",
        "Pragma": "no-cache",
        "Referrer-Policy": "strict-origin",
        "X-Content-Type-Options": "nosniff",
        "X-Frame-Options": "DENY",
        "Content-Security-Policy": (
            f"script-src 'nonce-{nonce}' 'strict-dynamic' https:; "
            "object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
        ),
    }


_STYLE = """
:root { color-scheme: light dark; --bg: #ffffff; --fg: #0a0a0a; --muted: #6b7280; --border: #e5e7eb; --primary: #171717; --primary-fg: #fafafa; }
@media (prefers-color-scheme: dark) { :root { --bg: #0a0a0a; --fg: #fafafa; --muted: #a1a1aa; --border: #27272a; --primary: #fafafa; --primary-fg: #171717; } }
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; background: var(--bg); color: var(--fg);
  font: 16px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; padding: 16px; }
main { max-width: 420px; width: 100%; text-align: center; }
h1 { font-size: 20px; margin: 0 0 8px; }
p { margin: 0 0 16px; color: var(--muted); }
.actions { display: flex; flex-direction: column; gap: 8px; }
button, a.button { display: block; min-height: 44px; padding: 12px 16px; border-radius: 999px; border: 1px solid var(--border); background: transparent;
  color: var(--fg); font: inherit; text-decoration: none; cursor: pointer; }
button.primary { background: var(--primary); color: var(--primary-fg); border-color: var(--primary); }
[hidden] { display: none !important; }
"""

# The page's own script: the web's pickDriveFiles and pickedFiles, then the
# import and the way back to the app.
_SCRIPT = """
(function () {
  var cfg = JSON.parse(document.getElementById("handoff-config").textContent);
  var status = document.getElementById("status");
  var again = document.getElementById("again");
  var back = document.getElementById("back");
  var busy = false;
  function done(params) {
    var query = new URLSearchParams(params).toString();
    window.location.replace(cfg.doneUrl + (query ? "?" + query : ""));
  }
  function say(text) { status.textContent = text; }
  // The Picker failing can be tried again; an import that failed has spent the page's ticket, so only Back is left.
  function failed(text, error, canRetry) {
    busy = false;
    say(text);
    again.hidden = !canRetry;
    back.hidden = false;
    back.setAttribute("href", cfg.doneUrl + "?error=" + encodeURIComponent(error));
  }
  function picked(docs) {
    var seen = {}; var files = [];
    for (var i = 0; i < docs.length; i++) {
      var d = docs[i];
      if (!d.id || seen[d.id] || d.mimeType === "application/vnd.google-apps.folder") continue;
      seen[d.id] = true;
      files.push({ id: d.id, name: (d.name || "").trim() || "Untitled", mime_type: d.mimeType || "" });
    }
    return files;
  }
  function importFiles(files) {
    say(files.length === 1 ? "Adding 1 file to your notebook…" : "Adding " + files.length + " files to your notebook…");
    fetch(cfg.importPath, {
      method: "POST",
      credentials: "omit",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ticket: cfg.ticket, files: files })
    }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (body) {
        if (response.ok) return done({ count: String(body.queued || 0), unsupported: String((body.unsupported || []).length) });
        var code = body && body.detail && body.detail.code;
        if (code === "google_drive_reconnect") return done({ error: "google_drive_reconnect" });
        if (code === "HANDOFF_EXPIRED") return failed("This page has run out. Go back to the app and pick again.", "expired", false);
        failed("Couldn't add the files. Go back to the app and try again.", "import_failed", false);
      });
    }, function () { failed("Couldn't reach Soloclef. Go back to the app and try again.", "import_failed", false); });
  }
  function open() {
    if (busy) return;
    busy = true;
    again.hidden = true;
    say("Opening Google Drive…");
    var view = new google.picker.DocsView(google.picker.ViewId.DOCS)
      .setIncludeFolders(true).setSelectFolderEnabled(false).setMode(google.picker.DocsViewMode.LIST);
    var picker = new google.picker.PickerBuilder()
      .addView(view)
      .enableFeature(google.picker.Feature.MULTISELECT_ENABLED)
      .enableFeature(google.picker.Feature.SUPPORT_DRIVES)
      .setMaxItems(cfg.maxItems)
      .setOAuthToken(cfg.accessToken)
      .setDeveloperKey(cfg.apiKey)
      .setAppId(cfg.appId)
      .setOrigin(window.location.origin)
      .setTitle("Choose files to add")
      .setSize(Math.max(320, window.innerWidth - 16), Math.round(window.innerHeight * 0.85))
      .setCallback(function (data) {
        var action = data[google.picker.Response.ACTION];
        if (action === google.picker.Action.PICKED) {
          picker.dispose();
          var docs = (data[google.picker.Response.DOCUMENTS] || []).map(function (doc) {
            return { id: doc[google.picker.Document.ID], name: doc[google.picker.Document.NAME], mimeType: doc[google.picker.Document.MIME_TYPE] };
          });
          var files = picked(docs);
          if (!files.length) return done({ count: "0", unsupported: String(docs.length) });
          importFiles(files);
        } else if (action === google.picker.Action.CANCEL) {
          picker.dispose();
          done({ error: "cancelled" });
        } else if (action === google.picker.Action.ERROR) {
          picker.dispose();
          failed("Couldn't open Google Drive. Try again.", "picker_failed", true);
        }
      })
      .build();
    picker.setVisible(true);
  }
  again.addEventListener("click", function () { open(); });
  window.handoffPickerLoaded = function () {
    gapi.load("picker", { callback: open, onerror: function () { failed("Couldn't open Google Drive. Go back to the app and try again.", "picker_failed", false); } });
  };
  var script = document.createElement("script");
  script.src = cfg.pickerScript;
  script.async = true;
  script.onload = function () { window.handoffPickerLoaded(); };
  script.onerror = function () { failed("Couldn't load Google Drive. Go back to the app and try again.", "picker_failed", false); };
  document.head.appendChild(script);
})();
"""


def render_picker_page(page: PickerPage, nonce: str) -> str:
    config = {
        "accessToken": page.access_token,
        "apiKey": page.api_key,
        "appId": page.app_id,
        "ticket": page.ticket,
        "importPath": page.import_path,
        "doneUrl": page.done_url,
        "maxItems": MAX_PICKED_FILES,
        "pickerScript": PICKER_SCRIPT_URL,
    }
    back = html.escape(f"{page.done_url}?error=cancelled", quote=True)
    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="strict-origin">
<title>Add from Google Drive · Soloclef</title>
<style nonce="{nonce}">{_STYLE}</style>
</head>
<body>
<main>
<h1>Add from Google Drive</h1>
<p id="status" role="status" aria-live="polite">Opening Google Drive…</p>
<div class="actions">
<button id="again" class="primary" type="button" hidden>Try again</button>
<a id="back" class="button" href="{back}">Back to the app</a>
</div>
</main>
<script id="handoff-config" type="application/json">{script_json(config)}</script>
<script nonce="{nonce}">{_SCRIPT}</script>
</body>
</html>
"""


def render_message_page(title: str, text: str, back_url: str | None, nonce: str) -> str:
    """A page with one sentence and, when the purpose is known, Back to the app."""
    link = (
        f'<div class="actions"><a class="button" href="{html.escape(back_url, quote=True)}">Back to the app</a></div>'
        if back_url
        else ""
    )
    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="strict-origin">
<title>{html.escape(title)} · Soloclef</title>
<style nonce="{nonce}">{_STYLE}</style>
</head>
<body>
<main>
<h1>{html.escape(title)}</h1>
<p role="status">{html.escape(text)}</p>
{link}
</main>
</body>
</html>
"""
