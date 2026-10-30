# Screenshots

Captures for the README. One vault for the whole set, so counts and names tell the same story.

**Rules for every shot**

- PNG, **dark theme**, **English UI** — do not mix themes or languages across the set.
- Browser chrome, OS taskbar and window borders cropped away; no cursor, no tooltips, no DevTools.
- No private vault data. Use the demo vault, not your working knowledge base.
- **Desktop:** 1440 × 900 CSS pixels, captured at 2× (2880 × 1800).
- **Mobile:** 390 × 844 CSS pixels, captured at DPR 2 (780 × 1688); the narrow layout must show the
  footer navigation, not a drawer.

## Desktop

| File | View | Must show |
| --- | --- | --- |
| `hero.png` | Claims | the list on the left, one claim open on the right: claim text, provenance, **Cluster peers** and **Same topic** |
| `desktop-start.png` | Start | the Ask box with the last questions, **Continue**, a non-empty **Needs you**, the newest sources with their step lights and the vault pulse |
| `desktop-sources.png` | Sources | the search/type/tag filter, the tag cloud, rows with human and auto tag chips, step lights, and an **attached** source with its paperclip |
| `desktop-ask.png` | Ask | a saved answer with inline citations, the answer written in the question's language, and its Ask ID |
| `desktop-assistant.png` | assistant | the field open over a list **and** the result banner after a request, e.g. `tag cloud · added ≤ 7 days · not finished` |
| `desktop-status.png` | Status | counts, the job queue, memory (live and peak, with the pass that caused the peak) and the maintenance actions |

## Mobile

| File | View | Must show |
| --- | --- | --- |
| `mobile-start.png` | Start | the footer navigation, the Ask box, Continue and Needs you |
| `mobile-claims.png` | one claim | the claim body with the provenance and peer cards stacked, and the footer navigation |
| `mobile-ask.png` | an answer | the answer with citations and the composer |
| `mobile-assistant.png` | assistant | the overlay field and its result banner above the keyboard-safe list |

## Optional

| File | View |
| --- | --- |
| `desktop-source.png` | one source: the **Summary** tab, the step lights and the rail with tags and ownership |
| `desktop-wiki.png` | the Wiki editor with title, tags and Markdown |
| `desktop-attention.png` | Attention with a failed step and a lint finding |
| `desktop-search.png` | the global search palette |
| `mobile-sources.png` | the Sources list with the filter chips |

## Where they appear

- `hero.png` — top of the README, width 920 px.
- The five other desktop files — the "A first look" table.
- The four mobile files — a centered row under the table, 240 px wide each.

## Redaction

Blurred (box blur, only the affected rectangles):

- source and document titles in every list, including `Continue`, `Recently arrived`, the Sources
  lens and the assistant result list;
- the URL or filename beneath a source title, because it repeats the title;
- provenance labels in the detail panes;
- in `hero.png`, every claim in the list except the selected one — title and topic line; only the
  open claim stays readable;
- the **Cluster peers** list in `hero.png` and `mobile-claims.png` — the claim text and its topic
  label, with the section header left visible;
- row icons in the Sources, Start and assistant lists — site favicons and the file-type glyphs alike;
- every saved query except the demo query (`What's most important about Clean Architecture?`), and
  the **Recently asked** chips on Start.

Removed without blur:

- in `desktop-status.png`, everything after `Thought Keeper` on the build line — version, uptime and
  address are gone.

Readable on purpose: claim text, the demo query and its answer, tags, counts and UI labels.

