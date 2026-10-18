"""The one Google scope Drive import asks for.

``drive.file`` reaches only the files a person picks in the Google Picker (or
that this app created). It is a non-sensitive scope, so a public app needs no
Google security assessment. Picking a folder does not reach the files in it;
people pick the files themselves. The account's address comes from Drive's
``about`` resource, which this scope may read, so no profile scope is needed.
"""

DRIVE_FILE_SCOPE = "https://www.googleapis.com/auth/drive.file"

SCOPES: list[str] = [DRIVE_FILE_SCOPE]
