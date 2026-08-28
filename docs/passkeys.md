# Passkey sign-in

A passkey replaces your password with the unlock you already use on the device — fingerprint,
face, or device PIN. It cannot be phished or reused on another site, it needs no external
service, and it works alongside the email and password form and any
[OAuth2 provider](/oauth) you have enabled. Nothing is removed by turning it on.

On the login screen it is one button: **Sign in with a passkey**. You do not type an email
address — the authenticator knows which account the passkey belongs to.

---

## Requirements

Browsers only run a passkey ceremony on a **hostname** served over a **secure connection**:

- `https://archive.example.com` — works.
- `http://localhost:8090` — works. `localhost` is the one exemption browsers make.
- `http://192.168.1.10:8090` — **does not work**, and neither does `https://192.168.1.10`. A
  passkey is bound to a domain name, so an IP address can never be used, over HTTPS or not.
- `http://archive.example.com` — does not work. Plain HTTP on a real hostname is not a secure
  context.

Where a passkey cannot be used, the button simply does not appear and the password form is
unaffected. Opening **More → Account** will tell you which of the conditions is not met.

Note that the app's own default address, `http://127.0.0.1:8090`, is a secure context but is
still an IP address. Open `http://localhost:8090` instead.

---

## Behind a reverse proxy

By default Lemmary derives everything from the request it receives: the relying-party ID is
the hostname (without the port), and the accepted origin is the scheme and host the request
arrived on. That is correct whenever the proxy forwards the original `Host`, and it also reads
`X-Forwarded-Proto` to learn that the browser used HTTPS.

Set these two only when that is not enough:

| Variable | What it is |
| --- | --- |
| `PASSKEY_RP_ID` | The relying-party ID: a bare domain name, no scheme and no port. Set it when the proxy does not forward the public `Host`, or to pin a parent domain (`example.com` while serving `app.example.com`). |
| `PASSKEY_ORIGINS` | Comma-separated full origins (scheme, host and port) allowed to complete a ceremony, e.g. `https://archive.example.com,https://www.archive.example.com`. Set it when the app is reachable at more than one origin, or when the proxy terminates TLS without setting `X-Forwarded-Proto`. |

Both are read from the environment, not from **Settings**. See
[Environment variables](/setup#environment-variables).

The page and the API do not have to share an origin. When the browser sends an `Origin` header
whose host is the relying party (or a subdomain of it), that origin is accepted too — which is
what lets a split dev setup, with the SPA on one port and the API on another, work without any
configuration.

---

## If the hostname changes, passkeys stop working

This is the one thing worth knowing in advance. **Every passkey is permanently bound to the
hostname it was created on.** Moving an install from `http://localhost:8090` to
`https://archive.example.com`, changing `PASSKEY_RP_ID`, or reaching the same install by two
different hostnames all have the same consequence: the passkeys enrolled under the old name
cannot be used under the new one, and they have to be added again.

They are not lost silently — sign in with your email and password, delete the stale entries
from **More → Account**, and add a new passkey.

---

## Step 1: add a passkey during first-launch setup

The setup wizard offers **Add a passkey** immediately after it creates your admin account.
It is optional; **Skip for now** goes straight on to the provider steps and the offer does not
come back. The step is hidden entirely on an address where a passkey cannot be created.

---

## Step 2: add a passkey later

1. Open **More → Account**.
2. Under **Passkeys**, choose **Add a passkey**.
3. Give it a name you will recognize — the field is pre-filled with a guess like
   *Chrome on Windows*. The name is Lemmary's own label; it is not shown by your device.
4. Confirm with your fingerprint, face, or PIN.

Add one per device. There is no self-signup in Lemmary, so a passkey can only ever be added to
an account that already exists — the same constraint that applies to [OAuth2](/oauth).

---

## Managing passkeys

**More → Account** lists every passkey on your account with the date it was added and when it
was last used. You can rename or remove any of them.

Removing a passkey here does not remove the entry your browser or operating system keeps in
its own keychain, and deleting it there does not remove it here. If a passkey has gone missing
from your device, delete it in Lemmary too so the list stays honest.

Deleting your last passkey is allowed, because email and password sign-in is still there. The
one case where Lemmary refuses is an account that has *no other way in* — identity/password
turned off in PocketBase and no OAuth2 provider linked. Add a password or another passkey
first.

---

## Notes

- A passkey here is a **primary** sign-in method, not a second factor. There is no
  password-plus-passkey step, and passkeys are not wired to PocketBase's own MFA.
- The **Admin** entry (PocketBase's own UI at `/_/`) always authenticates with the superuser
  password. Passkeys are registered against the `users` collection, so an admin can never lock
  themselves out of the server with a passkey mistake.
- Passkeys are **not** part of a library export. The export is a portable archive of your
  documents; a passkey is tied to one hostname and to hardware, so a restored copy could never
  authenticate and would only clutter the list. A full PocketBase backup (**Admin → Settings →
  Backups**) does include them, which is the right vehicle — restoring one puts the install
  back on the same hostname.
- Paperless-ngx clients continue to sign in with a username and password. A WebAuthn ceremony
  needs a browser, so there is nothing to add to that API.

---

## Troubleshooting

**The passkey button is not on the login screen.** Either the address cannot carry a passkey
(see [Requirements](#requirements)) or nobody has enrolled one yet. Sign in with your password
and add one from **More → Account**.

**"Passkeys need a hostname, not an IP address."** You are reaching the app by IP. Use its
hostname, or set `PASSKEY_RP_ID` and `PASSKEY_ORIGINS`.

**"Passkeys need the hostname localhost rather than a loopback IP address."** Open
`http://localhost:8090` instead of `http://127.0.0.1:8090`.

**"Passkey sign-in was cancelled or timed out."** The prompt was dismissed, or it was left
open too long. Cross-device sign-in via QR code can take a while — that is normal, and the
button keeps waiting.

**"This device already has a passkey for this account."** Nothing to do; use the one you have,
or remove it from **More → Account** first.

**"That passkey is no longer valid on this server."** The account behind it is gone, or the
hostname changed. Sign in another way and remove the entry.
