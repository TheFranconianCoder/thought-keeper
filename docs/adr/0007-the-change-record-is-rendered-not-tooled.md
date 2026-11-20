# 0007 — The change record is rendered in the PWA, not diffed by a tool

Status: accepted, 2026-11-20.

## Context

A material change of a URL page leaves `<hash>.changes.md` (ADR 0002): the time, the title before and
after, the word measurement and a unified diff of the readable body. The **Changes** tab rendered
that file verbatim in a `<pre>`, so the only signal was the `+`/`-` at the start of a line. The
reader opened the tab because a row said **updated** and could not see what moved.

A structural diff — **difftastic** was the concrete idea — would mark the changed syntax, not the
changed lines. It is a Rust binary with a tree-sitter grammar per language, driven as an external
process, and it would need the page's original bytes. The vault does not keep them: the record is the
old body's only witness, which is exactly why it is a record and not a cache. The stack is also
locked against a new external tool for a UI detail (poppler, tesseract and the optional Lightpanda
are the named exceptions), and a wrapper around difftastic would buy tree movement in prose at the
price of a second runtime to ship.

## Decision

- **The PWA parses the record it already reads.** `web/src/lib/changes.ts` turns `<hash>.changes.md`
  into a summary and typed, numbered hunks; `ChangeRecord.svelte` renders it. No server change and no
  new file format.
- **Color carries the line kind**: removed on a red wash, added on a green one, with an old and a
  new line-number gutter. The summary repeats the `+n`/`−m` counters the source strip wears.
- **Word-level emphasis stands in for the structural diff.** Within a change block, paired removed
  and added lines are compared token by token and the differing words are marked. That is the part
  of difftastic a reader of prose actually uses; it needs no grammar and no process.
- **The raw record stays reachable** behind a disclosure, and it is also the fallback: a record that
  does not parse renders as its bytes, never as half a diff.

## Consequences

- The record's sentences are a de-facto parse contract. Changing them in Go means teaching the
  parser; the fallback keeps the tab readable in the meantime, and a future structured sidecar can
  replace the parser without changing the view's model.
- The emphasis is lexical, not syntactic: a moved paragraph is a deletion and an insertion, not a
  tree move. That is acceptable for a record built from readable Markdown and keeps the app free of a
  language-specific diff engine.
