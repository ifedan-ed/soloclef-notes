"""Connecting a Nextcloud account in one click, with Nextcloud's Login Flow v2.

The backend asks Nextcloud for a login URL, the person signs in there (SSO
included) and grants access, and the backend polls until Nextcloud hands over
an app password. The poll token and the app password never reach the browser.
"""
