# Contributing

Thanks for wanting to help. Please read this before writing code: **only
maintainers can open pull requests in this repository.** It's how the project
works, not a judgement on anyone's skill.

## Why only maintainers open pull requests

Writing code is no longer the expensive part. A good description turns into a
working diff in minutes.

The expensive part is knowing that the diff is right:

- reading every line and checking it fits the rest of the codebase
- running it against the heavy end-to-end suite
- keeping the UI, API errors and docs in sync across English, German and Russian
- making sure existing installs upgrade cleanly: migrations, settings, stored data
- testing the change by hand in the running app, beyond what the suites cover

A pull request skips none of that. Verifying someone else's diff, written
without the context of where the project is going, costs more than generating
the same change from a clear spec and verifying that. So the code in a PR isn't what's valuable. The understanding
behind it is, and that fits better in an issue.

## How to contribute

Open an [issue](https://github.com/buldezir/lemmary/issues/new/choose). The
most useful issue is one precise enough to paste into a coding agent as its
prompt. If an agent could build the right thing from it with no follow-up
questions, it's a great issue.

### Feature requests and changes

Write it as a prompt for an agent. The **Feature or change** issue template
asks for three parts:

- **Goal.** What you're trying to do, and why. One or two sentences.
- **Behaviour.** What happens today and what should happen instead. Name
  screens, settings or API calls exactly; be concrete about labels, defaults,
  what an admin sees and what a regular user sees. Mention the files or
  endpoints involved if you know them.
- **Acceptance.** Steps a person could follow to check it's done and what
  they'd see at the end. Include edge cases (empty states, large inputs, other
  languages, file types, providers, documents created before the change) and
  what this should *not* change.

A short example:

```markdown
**Goal:** See how long a document is without opening it.

**Behaviour:** Document cards in the list show the title, date, tags and either
the summary or a preview of the first page; the page count is only visible
inside the PDF viewer. PDF cards should show "12 pages" next to the date, and
"1 page" for single-page PDFs. Images and other files show nothing. The cards
live in `frontend/src/components/DocumentCard.tsx`, and the PDF split tool
already counts pages, so there may be something to reuse.

**Acceptance:** Upload a 3-page PDF and a JPEG. The PDF's card shows "3 pages";
the JPEG's card shows no count. Switching the UI to German or Russian
translates the label. Documents uploaded before this change get a count
without being reprocessed. A PDF that fails to parse shows no count instead of
"0 pages". Sorting or filtering by page count is out of scope.
```

### Bug reports

Include:

- the version you run (shown in the page footer)
- how you run it: Docker or from source, and which AI and OCR providers
- steps to reproduce, from a fresh page load
- what you expected and what happened instead
- relevant logs, with API keys, tokens and personal data removed

### Already wrote the code?

Link your fork or branch in the issue. Working code is a useful reference for
the spec, but the change will be reimplemented rather than merged.
