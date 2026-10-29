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

A pull request skips none of that. Verifying someone else's diff, 
written without the context of where the project is
going, costs more than generating the same change from a clear spec and
verifying that. So the code in a PR isn't what's valuable. The understanding
behind it is, and that fits better in an issue.

## How to contribute

Open an [issue](https://github.com/buldezir/lemmary/issues/new/choose). The
most useful issue is one precise enough to paste into a coding agent as its
prompt. If an agent could build the right thing from it with no follow-up
questions, it's a great issue.

### Feature requests and changes

Write it as a prompt for an agent. The **Feature or change** issue template
asks for these parts:

- **Goal.** What you're trying to do, and why. One or two sentences.
- **Current behaviour.** What happens today, with screens, settings or API
  calls named exactly.
- **Wanted behaviour.** What should happen instead. Be concrete: labels,
  defaults, what an admin sees and what a regular user sees.
- **Where.** Screens, endpoints, settings or files involved, if you know them.
  Optional.
- **Acceptance.** How to check it's done: steps a person could follow and what
  they'd see at the end.
- **Edge cases.** Empty states, large inputs, other languages, file types,
  providers, existing documents created before the change.
- **Out of scope.** What this should *not* change.

A short example:

```markdown
**Goal:** See how long a document is without opening it.

**Current behaviour:** Document cards in the list show the title, date, tags
and either the summary or a preview of the first page. The page count is only
visible inside the PDF viewer.

**Wanted behaviour:** PDF cards show "12 pages" next to the date. Single-page
PDFs show "1 page". Images and other files show nothing.

**Where:** The document list and its cards (`frontend/src/components/DocumentCard.tsx`).
The PDF split tool already counts pages, so there may be something to reuse.

**Acceptance:** Upload a 3-page PDF and a JPEG. The PDF's card shows "3 pages";
the JPEG's card shows no count. Switching the UI to German or Russian
translates the label.

**Edge cases:** Documents uploaded before this change get a count without being
reprocessed. A PDF that fails to parse shows no count instead of "0 pages".

**Out of scope:** Sorting or filtering by page count.
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
