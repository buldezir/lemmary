# OAuth2 / SSO sign-in

The login screen mirrors whatever the PocketBase `users` collection accepts. Enable an
OAuth2 provider there and a **Continue with …** button appears next to the email and
password form — no rebuild or environment variable needed.

---

## Step 1: Enable a provider in PocketBase

1. Open the PocketBase admin UI (the **Admin** entry in the app's **More** menu, or
   `/_/` on the server) and sign in with the admin account created during setup.
2. Go to **Collections → users → Options → OAuth2**.
3. Toggle **Enable** and add a provider (Google, GitHub, OIDC, …).
4. Fill in the **Client ID** and **Client secret** from the provider's console, and
   register PocketBase's redirect URL with that provider:

   ```
   https://<your-app-host>/api/oauth2-redirect
   ```

5. Save the collection, then reload the app's login screen.

Every enabled provider gets its own button, labelled with the provider's display name.
Sign-in happens in a popup window; the app picks the session up as soon as it closes.

---

## Step 2: Give the account a user record

The `users` collection has no create rule — this app has no self-signup — so OAuth2 can
sign in accounts that **already exist** but cannot create new ones. PocketBase matches
the provider's email against existing `users` records, so:

- The admin account created during setup can sign in with OAuth2 right away, as long as
  the provider account uses the same email address.
- For anyone else, create the user first: **Collections → users → New record**, with the
  email address that provider account will report. The password set there is never used
  for OAuth2 sign-in.

An account with no matching `users` record gets `Failed to authenticate.` from
PocketBase, which the login screen reports along with this hint.

---

## Optional: OAuth2 only

Turning **Identity/password** off under **Collections → users → Options** hides the email
and password form and leaves only the provider buttons. Two things to know first:

- Admins keep a way in through the PocketBase admin UI at `/_/`, which authenticates
  against `_superusers` and is unaffected by this setting.
- The app still shows the password form if you disable identity/password *and* leave no
  OAuth2 provider enabled, so the setting cannot lock everyone out.
