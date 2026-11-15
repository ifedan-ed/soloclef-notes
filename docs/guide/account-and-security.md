# Account and security

Everything in your account is yours alone: notebooks, documents, notes,
chats, connectors, automations, prompts and the assistant's memory. Nobody
else on the server can see it. The one exception is a Studio output you
share with a public link (see [Studio](studio.md#sharing-an-output)).

## Signing in

Sign in with your email and password. If the server offers single sign-on,
its button is above the password form.

If you have no account, choose Sign Up when the server offers it. Otherwise
choose Request access, which opens the server's contact page.

A password needs at least 8 characters, at most 256, and must not be your
email address. Each sign-in form takes 10 tries a minute from one place;
past that, it asks you to wait a minute.

### Forgotten password

Forgot Password?, on the sign-in page, emails you a link to choose a new
password. The link works once, for an hour. Setting the new password signs
you out everywhere. If the server doesn't send email, the page says that
password reset by email isn't available there: ask its administrator to set
a password for you.

### Single sign-on

Single sign-on signs you in to the account with the same email address. For
that, the provider must confirm your address, and so must Soloclef:

- An account you made with the sign-up form is not confirmed at first. Single
  sign-on then says "Account not confirmed". Sign in with your password, or
  use Forgot Password? once, which confirms the address.
- If you have no account yet, the first sign-on makes one when the server
  lets people sign up. Otherwise it says "No account for this address".

Once linked, single sign-on keeps opening the same account, even if your
address at the provider changes.

An account made by single sign-on has no password. To also sign in with a
password, set one under Settings, Security on the website, or in the phone
app under More, Profile, Set a password. Unless you signed in with single
sign-on in the last ten minutes, you are asked to confirm it's you with the
provider first. On the website that takes you to the provider and back to
Security, where you type the new password again; set it within ten minutes.
Forgot Password? also works, when the server sends email.

## Staying signed in

| Where | How long |
| --- | --- |
| A browser | 14 days without use, and 30 days at most. |
| The phone app | Until it goes 180 days without use. A phone you use stays signed in. |

These end a sign-in at once, wherever it is:

- signing out on that device;
- signing a device out from the list of signed-in devices;
- Sign out everywhere;
- a password reset or change. A change keeps the device that made it signed
  in;
- the administrator setting a new password for you;
- deleting your account;
- a sign-in token used twice, which suggests a copy of it is in use.

An ended sign-in is refused on its very next request.

Logout, in the website's account menu, signs out that browser. The rest is
under Settings, Security on the website, and in the phone app:

- Signed-in devices lists every browser and phone your account is signed in
  on, with when each signed in and was last used. The website marks its own
  row This browser; the app marks This phone. Sign out any one of them; that
  device has to sign in again.
- Sign out everywhere ends every sign-in, the one you are using included.
  Phones get no notifications until they sign in again. In the app it is at
  the foot of Signed-in devices.
- Change password asks for your current password and the new one twice.
  Every other device is signed out; the browser or phone you change it on
  stays signed in. In the app it is under More, Profile.

## Settings

Open your avatar at the bottom of the left rail and choose Settings. The
sections are:

| Section | What it holds |
| --- | --- |
| Profile | Your display name, your email, and Delete account. |
| Security | Your password, signed-in devices, and Sign out everywhere. See [Staying signed in](#staying-signed-in). |
| Appearance | Whether messages show their time. |
| Notifications | Which kinds of notification are pushed, and quiet hours. See [Notifications](notifications.md). |
| API access | The switch that lets API keys and agents act for you, and your API keys. See below. |
| Models | Which model does each job, and your own model connections. See [Models](models.md). |
| Instructions | Standing instructions, and the Citations switch. See [Memory and instructions](memory-and-instructions.md). |
| Memory | What the assistant remembers about you. |
| My prompts | Prompts you use with / in the chat. |
| Connectors | Google Drive, Nextcloud, mailboxes, Exa, Tavily and MCP servers. See [Connectors](connections.md). |
| Automations | See [Automations](automations.md). |
| Public links | Every Studio output you have shared. |
| Agent permissions | Which tools ask you before they run. See [Chat and citations](chat-and-citations.md#ask-first). |

Light, dark or system theme, and the language, are in the avatar menu.

## API access

API keys and AI agents can only act for you while API and agent access, under
Settings, API access, is on. It is off for a new account. While it is on,
they can read and change everything in your account. Turn it off to cut every
agent off at once, even ones with a valid key.

To make a key, choose Create API key, give it a name and, if you like, an
expiry in days. Copy the key when it is shown: it is shown only once. The list
shows each key's first characters, when it expires and when it was last used.
Delete a key to stop it working. See
[Connect your agent](connect-your-agent.md) for what to do with it.

## Deleting your account

Settings, Profile, Delete account. Type DELETE to confirm, then choose Delete
my account. You are signed out everywhere at once. Everything the account
owns is then erased: notebooks, notes, chats, documents and their files,
connectors, mailboxes, drafts and API keys. This can't be undone.
