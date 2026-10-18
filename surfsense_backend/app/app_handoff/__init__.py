"""The phone app's one browser hand-off (plan items B2, B6 and B10).

Some things only run on a web page in a browser the person is signed in
to: Google's Picker (it is Google's own JavaScript), Google's consent page,
Microsoft's. The app cannot put its session in that browser, and no token
may travel in a URL. So the app asks for a hand-off code with its bearer
token, opens the code's page in the phone's browser sheet, and the server,
holding the code's grant, starts the flow in that browser::

    POST /api/v1/app/handoff {purpose, workspace_id, notebook_id?, account_id?,
                              code_challenge?}  (bearer)
        -> {code, expires_in, path}       60 s, single use, one purpose
    GET  /api/v1/app/handoff/{code}       (the browser sheet; the code is spent)
        -> drive_picker:   a page that runs Google's Picker with the person's
                           own Drive grant, imports what was picked into the
                           notebook, and goes back to
                           soloclef://drive/done?count=N&unsupported=M
        -> drive_connect,  303 to Google's consent page or Microsoft's
           microsoft_mail: sign-in, the attempt's signed cookie set in this
                           same browser (each provider's connect_attempt.py);
                           the callback saves nothing and goes back to
                           soloclef://connectors/done?purpose&status=confirm
                           &completion=… (or ?purpose&status=<refusal>)
    POST /api/v1/app/handoff/complete {completion, code_verifier}  (bearer)
        -> drive_connect, microsoft_mail: saves the held Drive or mailbox (5
           minutes, single use) for the account that asked for the code, when
           the verifier is the one behind its code_challenge (required for both)
    POST /api/v1/app/handoff/drive-picker/import {ticket, files}
        -> the Picker page's import: the ticket (10 minutes, single use) in
           the page's body stands for the person, the workspace and the notebook

Why Microsoft mail and Drive connect need the confirmation: the page takes
no credentials, so whoever opens a code's link goes through the provider in
their own browser. Without it, a link sent to someone else (or an account's
own page minting a fresh code for every visitor) would connect the visitor's
mailbox or Drive to the link's account. Drive joined on 2026-10-11, when the
website's own Drive connect was bound to the browser that starts it too.

``codes.py`` holds the codes, tickets and completions, ``purposes.py`` what a hand-off can
be for (and the Picker's return), ``returns.py`` where a connector sign-in
goes back to, ``picker_page.py`` the Picker's page, ``routes.py`` the doors.
Limits are in custom/README.md ("The phone app's browser hand-off").

Two tracks wrote this package (the Drive Picker with the app's Sources, Drive
connect and Microsoft mail with its Connectors); feat/sn-backend made it one.
"""
