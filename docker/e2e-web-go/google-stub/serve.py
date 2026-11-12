"""The stand-in Google, served the two ways the Go stack reaches Google.

The stand-in itself is google_server.py beside this file (consent page,
token, revoke, the Drive v3 calls), mounted with it at /stub. What this
adds is a second listener.

The Go code does not take Google's addresses from settings: they are
constants (soloclef_api/internal/driveconnect, soloclef_worker/internal/
drivesync), because a deployment that could point "Google" somewhere else
is a deployment that can be made to send a person's grant to somebody. So
the test cannot hand the API a stub address. It makes the real names lead
here instead: this container answers to oauth2.googleapis.com and
www.googleapis.com on the stack's network (the overlay's aliases), over TLS
with a certificate from the stack's own test authority, which the API and
the worker are told to trust through SSL_CERT_FILE. Production code is
untouched, and what is tested is the code that runs.

    :8080  plain HTTP   the consent page, for the browser, through Caddy at /__google__/
    :443   TLS          token, revoke and Drive, for the API and the worker

Both listeners share one state, so a code the consent page hands out is one
the token endpoint knows.
"""
import os
import ssl
import sys
import threading
from http.server import ThreadingHTTPServer

sys.path.insert(0, "/stub")
import google_server  # noqa: E402  (the path above is where it is mounted)


def main():
    state = google_server.StubState(public_url=os.environ["GOOGLE_STUB_PUBLIC_URL"].rstrip("/"))
    handler = google_server.make_handler(state)

    secure = ThreadingHTTPServer(("0.0.0.0", 443), handler)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(os.environ["GOOGLE_STUB_TLS_CERT"], os.environ["GOOGLE_STUB_TLS_KEY"])
    secure.socket = context.wrap_socket(secure.socket, server_side=True)
    threading.Thread(target=secure.serve_forever, daemon=True).start()

    ThreadingHTTPServer(("0.0.0.0", 8080), handler).serve_forever()


if __name__ == "__main__":
    main()
