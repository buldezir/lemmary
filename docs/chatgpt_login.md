# ChatGPT sign-in

Lemmary can run its language-model work on a **ChatGPT Plus, Pro or Business
subscription** instead of a metered API key. You sign in once with a device
code, bind chat, extraction, Deep Search and OCR to it, and those calls come out
of the seat you already pay for rather than out of API credits.

It is off unless you turn it on, and there are good reasons for that. Read the
whole page before you do.

## What it can and cannot do

| | |
| --- | --- |
| Document chat | ✅ |
| Metadata extraction | ✅ |
| Deep Search (and its per-document helper) | ✅ |
| OCR | ✅ — a PDF or an image, up to 10 MB, read by the bound model |
| Embeddings | ❌ — keep a keyed provider or the sidecar bound |

OCR works the same way it does on OpenAI or OpenRouter: the file goes to the
model as an attachment and the model transcribes it. So the whole pipeline can
run on the subscription — which means an instance whose only AI credential is a
ChatGPT seat is a complete install, with no API key anywhere.

Embeddings are the exception, and not a matter of degree: the endpoint has no
`/embeddings` at all, so Settings refuses that binding. Deep Search still works
without them — it falls back to keyword matching — but its dense half needs a
keyed provider or the [local embeddings sidecar](/local_embeddings).

Two things worth knowing before you put OCR here. A subscription's quota is a
window rather than a meter, and OCR is the heaviest thing Lemmary does per
document, so a bulk import can spend a window quickly. And these are general
models being asked to read a scan, not a document endpoint: Mistral's OCR is
purpose-built for the job and the Docling sidecar does it without leaving your
host. Either is the better OCR if you have it.

## What you are agreeing to

This works by presenting Lemmary as one of OpenAI's own Codex clients: it uses
the Codex client id to sign in and the Codex `originator` header to make
requests. Those endpoints are undocumented and reserved for OpenAI's own
clients.

Three consequences worth being clear about:

- **The account is yours to risk.** OpenAI can treat a third-party client using
  subscription credentials as a terms violation. Nothing here hides what
  Lemmary is doing, and nobody can promise how OpenAI will treat it.
- **It can break without warning.** Any change on OpenAI's side can start
  refusing these requests. The failure shows up as a provider error on the next
  document, not as a silent fallback to a billed provider.
- **Quota is a window, not a meter.** A subscription's allowance refills over
  five-hour and weekly windows. Deep Search fans out across documents and can
  spend a window quickly; if that becomes a problem, move the **Deep Search
  helper** binding back to a keyed provider first — it does the bulk reading.

This is why the SDK never appears on a managed instance: there, the tenant is
not the party whose account would be at stake.

## Turning it on

### 1. Allow device-code sign-in on the ChatGPT account

Device-code login is off by default for everybody. It is what lets you approve a
sign-in from any browser, which is the only way this works on a server.

- **Personal account** — ChatGPT → **Settings** → **Security** → enable device
  code authorization.
- **Team, Enterprise or Edu** — a workspace admin has to grant it in the
  workspace permissions; the personal toggle is greyed out or absent.

If you skip this, Lemmary's sign-in fails with a message naming the setting.

### 2. Set the flag

```bash
AI_CHATGPT_LOGIN=1
```

Absent means off. A value it cannot read — `AI_CHATGPT_LOGIN=ture` — is an error
rather than a silent off, the same as `AI_MANAGED`. Setting it together with
`AI_MANAGED=1` refuses to start.

Restart the app. **Settings → Providers** now offers a **ChatGPT subscription**
SDK.

### 3. Add the provider and sign in

1. **Settings → Providers → Add provider**, SDK **ChatGPT subscription**. There
   is no API key field. Save it.
2. The saved row grows a **Sign in with ChatGPT** button. Press it and Lemmary
   shows a short code and a link.
3. Open the link in any browser signed in to the ChatGPT account, enter the
   code, approve. The code is good for about fifteen minutes.
4. The row reads **signed in**, with the account and plan beside it.

### 4. Bind the models

Under **Settings → Models**, point **Chat**, **Extraction**, **Deep Search**,
the **Deep Search helper** or **OCR** at the new provider and pick a model. The
picker is served locally — the endpoint publishes no catalogue — and the
**Custom model id** box takes anything the list does not name. Which models the
account may actually use depends on its plan; one it may not use fails on the
first request with the backend's own message.

The same list is offered for OCR as for chat, because the Codex catalogue says
nothing about which models read a file. Pick a full model rather than a `mini`
one if the scans are poor.

Leave **Embeddings** where it is.

### Setting up a fresh instance this way

The setup wizard offers **ChatGPT subscription** too, once `AI_CHATGPT_LOGIN=1`
is set, so a first-boot instance can be configured with no API key at all.
Choosing it saves the provider row and then holds the step open for the
sign-in — the token needs a row to be stored against — and setup carries on to
the model bindings once you have approved the code.

## Signing out, and signing in as somebody else

**Sign out** on the provider row clears the stored token and leaves the row.
Signing in again is the same two clicks, so switching accounts needs no new
provider.

The bindings stay pointed at the row while it is signed out; those features
report an unconfigured provider until somebody signs in again.

## Where the token lives

In the `oauth` column of the provider row, beside the API keys of every other
provider. Like a key it is never returned by the API — Settings sees only
whether a row is signed in, and the account name it was signed in with.

The access token lasts about an hour and is refreshed in place, roughly once an
hour, for as long as the instance runs. If you use [encryption at
rest](/encryption), the token is covered by it exactly as an API key is.

## Troubleshooting

**"device code sign-in is not enabled for this account"** — step 1 above. On a
Team, Enterprise or Edu workspace only an admin can grant it.

**The code expired** — it is good for about fifteen minutes. Press **Sign in
with ChatGPT** again for a new one.

**403 on every request after a successful sign-in** — usually the account's plan
not covering the bound model. Try the default model, or a smaller one.

**Requests start failing for everyone at once** — most likely the quota window,
or a change on OpenAI's side. Rebind chat and Deep Search to a keyed provider
while you work out which.

**OCR returns empty or invented text** — the model was handed the file but
could not read it, or was a `mini` model on a poor scan. Try a larger model
first. `OCR_SDK=chatgpt` is refused in `.env`, by the way: the environment can
carry a key but not a sign-in, so OCR is bound to this provider from Settings.

**"LLM OCR does not support mime type ..."** — the attachment path takes PDFs
and images only, the same as OpenAI and OpenRouter. Anything else needs Mistral,
Google Vision or the Docling sidecar.

**Extraction results got worse after switching** — extraction asks for JSON, and
a backend that will not honour the request is answered in plain text and parsed
leniently. If the metadata is thinner than it was, put **Extraction** back on a
keyed provider and leave chat and Deep Search on the subscription.
