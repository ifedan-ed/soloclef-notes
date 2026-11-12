package apphandoff

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/driveconnect"
)

// The Drive Picker's page, as a string the browser sheet shows.
//
// Google's Picker is Google's own JavaScript and needs a page to run on,
// with the account's short-lived Drive access token (drive.file: it reaches
// only what is picked). The page opens the Picker at once, posts the picks
// with the page's ticket to the import, and goes back to the app with how
// many were added. Cancel goes back with error=cancelled; anything else
// says what went wrong, with Back to the app, and Try again when the
// Picker itself failed (a failed import has spent the ticket).
//
// Nothing secret is in the address. The token and the ticket are in the
// page's body, as JSON a script reads; the page is never cached or framed,
// tells other sites only its origin (Google checks the Picker's API key
// against it; the path, which names the spent code, never leaves), and
// runs only scripts carrying its nonce ('strict-dynamic' lets Google's
// loader bring the Picker's own).

// pickerScriptURL is Google's loader.
const pickerScriptURL = "https://apis.google.com/js/api.js"

// maxPicked is what one pick may hold: the import's own limit.
const maxPicked = 50

func newNonce() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// securityHeaders keep the page to itself: no caching, no framing, only
// the origin as referrer, only its own scripts and what they load.
func securityHeaders(header http.Header, nonce string) {
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Pragma", "no-cache")
	header.Set("Referrer-Policy", "strict-origin")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Content-Security-Policy", "script-src 'nonce-"+nonce+"' 'strict-dynamic' https:; "+
		"object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
}

// pageStyle follows the device's light or dark setting.
const pageStyle = `
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
`

// pickerScript is the web's Picker flow on its own page, then the import
// and the way back to the app. A refusal is read from the error envelope's
// code (errorenvelope: {"error": {"code": …}}).
const pickerScript = `
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
    say(files.length === 1 ? "Adding 1 file…" : "Adding " + files.length + " files…");
    fetch(cfg.importPath, {
      method: "POST",
      credentials: "omit",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ticket: cfg.ticket, files: files })
    }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (body) {
        if (response.ok) return done({ count: String(body.queued || 0), unsupported: String((body.unsupported || []).length) });
        var code = body && body.error && body.error.code;
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
  var script = document.createElement("script");
  script.src = cfg.pickerScript;
  script.async = true;
  script.onload = function () {
    gapi.load("picker", { callback: open, onerror: function () { failed("Couldn't open Google Drive. Go back to the app and try again.", "picker_failed", false); } });
  };
  script.onerror = function () { failed("Couldn't load Google Drive. Go back to the app and try again.", "picker_failed", false); };
  document.head.appendChild(script);
})();
`

// pickerPage writes the Picker's page for session, with ticket for its
// one import.
func (r Routes) pickerPage(c call, session driveconnect.PickerSession, ticket string) {
	nonce, err := newNonce()
	if err != nil {
		r.failed(c, "could not make the page's nonce", err)
		return
	}
	// encoding/json escapes <, > and & (and U+2028, U+2029), so no value
	// can close the script element it is written into.
	config, err := json.Marshal(map[string]any{
		"accessToken":  session.AccessToken,
		"apiKey":       session.APIKey,
		"appId":        session.AppID,
		"ticket":       ticket,
		"importPath":   ImportPath,
		"doneUrl":      appreturn.Drive,
		"maxItems":     maxPicked,
		"pickerScript": pickerScriptURL,
	})
	if err != nil {
		r.failed(c, "could not write the page", err)
		return
	}
	back := html.EscapeString(appreturn.DriveReturn(url.Values{"error": {"cancelled"}}))
	var page strings.Builder
	page.WriteString(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="strict-origin">
<title>Add from Google Drive · Soloclef</title>
<style nonce="` + nonce + `">` + pageStyle + `</style>
</head>
<body>
<main>
<h1>Add from Google Drive</h1>
<p id="status" role="status" aria-live="polite">Opening Google Drive…</p>
<div class="actions">
<button id="again" class="primary" type="button" hidden>Try again</button>
<a id="back" class="button" href="` + back + `">Back to the app</a>
</div>
</main>
<script id="handoff-config" type="application/json">` + string(config) + `</script>
<script nonce="` + nonce + `">` + pickerScript + `</script>
</body>
</html>
`)
	securityHeaders(c.response.Header(), nonce)
	c.response.WriteHeader(http.StatusOK)
	_, _ = c.response.Write([]byte(page.String()))
}

// messagePage writes a page with one sentence: what the sheet shows when
// the Picker's code has run out, before there is anything to go back with.
func (r Routes) messagePage(c call, status int, title, text string) {
	nonce, err := newNonce()
	if err != nil {
		r.failed(c, "could not make the page's nonce", err)
		return
	}
	page := `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="strict-origin">
<title>` + html.EscapeString(title) + ` · Soloclef</title>
<style nonce="` + nonce + `">` + pageStyle + `</style>
</head>
<body>
<main>
<h1>` + html.EscapeString(title) + `</h1>
<p role="status">` + html.EscapeString(text) + `</p>
</main>
</body>
</html>
`
	securityHeaders(c.response.Header(), nonce)
	c.response.WriteHeader(status)
	_, _ = c.response.Write([]byte(page))
}
