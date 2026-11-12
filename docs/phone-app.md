# Phone app

[![Download on the App Store](images/app-store-badge.svg){ width="160" }](https://apps.apple.com/app/soloclef/id6821393434)

Soloclef for iPhone: <https://apps.apple.com/app/soloclef/id6821393434>

The app works with any Soloclef server. It has your notebooks, sources,
uploads from the phone, chat with citations, every Studio output (audio plays
in the background, with lock-screen controls), notes, and changes made offline
that are sent when the phone is back online.

## Connecting it to your server

To use your own server:

1. On the sign-in screen, tap Change server.
2. Enter your server's address, for example `notes.example.org`. The app
   checks that it is a Soloclef server and reachable over https.
3. Sign in with your email and password, or with your server's single sign-on
   button if it has one.

The app keeps that server until you change it again (More → Server).
Changing servers signs you out of the old one first and removes everything the
app kept from it.

## What your server needs

- An https address with a certificate phones trust (the app talks to servers
  over https only), set as `SOLOCLEF_API_PUBLIC_URL`.
- For the single sign-on button in the app: `SOLOCLEF_API_OIDC_APP_REDIRECTS`
  listing `soloclef://auth` (see [Sign-in](sign-in.md)).

The app registers for push notifications and keeps its notification choices,
but this version of the server sends no pushes yet.

## Android

There is no Play Store build yet. You can build the app yourself from
`soloclef_mobile` with Expo; see its README.
