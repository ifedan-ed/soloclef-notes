# Phone app

[![Download on the App Store](images/app-store-badge.svg){ width="160" }](https://apps.apple.com/app/soloclef/id6821393434)

**Soloclef** for iPhone: <https://apps.apple.com/app/soloclef/id6821393434>

The app works with any Soloclef server. It has your notebooks, sources,
uploads from the phone, chat with citations, every Studio output (audio plays
in the background, with lock-screen controls), notes, and changes made offline
that are sent when the phone is back online.

## Connecting it to your server

The app opens on its author's own private server, where accounts are by
invitation (**Request access**). To use yours:

1. On the sign-in screen, tap **Change server**.
2. Enter your server's address, for example `notes.example.org`. The app
   checks that it is a Soloclef server and reachable over https.
3. Sign in with your email and password, or with your server's single sign-on
   button if it has one.

The app keeps that server until you change it again (**More → Server**).
Changing servers signs you out of the old one first and removes everything the
app kept from it.

## What your server needs

- An https address (the app talks to servers over https only).
- `SURFSENSE_PUBLIC_URL` set to that exact address, for push notifications.

## Android

There is no Play Store build yet. You can build the app yourself from
`surfsense_mobile` with Expo; see its README.
