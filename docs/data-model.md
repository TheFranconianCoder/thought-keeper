# Thought Keeper — data model and index

Entities, identifiers, the SQLite schema and the derived values that are never stored. The vault
files themselves are described in `storage.md`; the index is a **cache** and never decides what is
true.

## 1. Entities

| Entity | Meaning |
| --- | --- |
| `Source` | An immutable input: `id`, `hash`, `kind`, `ext`, `path`, `url`, `title`, `original_name`, `tags` (human labels), `auto_tags` (derived labels) and `auto_tags_at`, `bytes`, `pages`, `tree_state`, `steps`, `url_status`, `url_checked_at`, `bookmark`, `meta`. |
| `SourceNode` | A tree node: `node_id`, `parent_id`, `depth`, `ord`, `title`, `summary`, `start_page`, `end_page`, `text_hash`. |
| `Atom` | One claim: `id`, `topic`, `claim`, `type`, `status`, `body`, `sources`. |
| `WikiPage` | User-authored Markdown: `id`, `title`, `tags` (human), `auto_tags` (derived), `urls`, `source` (stable `WikiHash`), timestamps, body. |
| `Cluster` | A titleless association: `id`, `atoms`, `signature`, `created`, `updated`. |
| `ClusterMembership` | One cluster→atom edge. |
| `Link` | An atom edge: `from_id`, `raw_target`, `to_id`, `alias`, `heading`, `occurrences`, `position`. |
| `PartState` | How far one part has been derived: `node_id`, `state`, `reason`, `atoms`, `run_id`. |
| `LintFinding` | Health result: `layer`, `kind`, `severity`, `subject_kind` (atom/cluster/source), `subject_id`, `message`. |
| `StepStatus` | One step's state for one source, persisted in the source's `.meta`. |
| `Vocabulary` | A namespace's canonical names and what they absorbed. Three namespaces: `topics`, `wiki-tags` and `source-tags`. |
| `Run` | One run: `op` (`pipeline` \| `query` \| `lint` \| `vocabulary` \| `tag` \| `summary`), `step` (capture/outline/distill/cluster), model, tokens, status, and the question for a query. |
| `StepRecord` | One tool call of a run: `tool`, argument/result digests, error flag, duration. |

## 2. Identifiers

- **Atoms, clusters, wiki pages and runs use ULID** (16-byte `BLOB`), Crockford Base32 on the wire.
- **Sources are addressed by SHA-256** (hex `TEXT`, unique). A URL's address is
  `sha256("url:" + url)`; a wiki page's representation is `WikiHash(id)`.
- **Topics and tags are names**, not ids.

## 3. Index schema

`index.db` lives in the vault root and belongs to exactly one vault. It is derived: `reindex` +
`rebuild-trees` rebuild it from `sources/`, `atoms/`, `wiki/`, `clusters/` and the vocabulary. The
index is disposable — deleting it loses nothing but time.

### Migrations

| File | What it does |
| --- | --- |
| `00001_initial_schema.sql` | the whole shape: tables, indexes, FTS5 tables, `PRAGMA user_version = 20261022` |
| `00002_run_op_tag.sql` | rebuilds `runs` so `op` also accepts `tag` (SQLite cannot alter a CHECK in place) |
| `00003_atoms_updated_index.sql` | an index on `atoms(updated_at)` for the incremental reads |
| `00004_run_op_summary.sql` | rebuilds `runs` so `op` also accepts `summary` |
| `00005_source_tags.sql` | adds `sources.tags` (a JSON array): the mirror of the human labels in the source record |
| `00006_source_auto_tags.sql` | adds `sources.auto_tags` and `auto_tags_at` (the agent's labels and the backfill marker) |

A schema change is a **new** migration; a change of vocabulary is done in the initial file with the
database thrown away. The initial file writes the shape marker (`PRAGMA user_version`), and
`persistence.Open` compares it with `persistence.SchemaVersion` (20261022).

A mismatch is a state the app **reports and never repairs**: the index lives in the user's vault.
The start-up log, `GET /api/v1/status` (`index.outdated`) and `thought-keeper status` carry the
sentence — which file it is and what to do ("delete it and reindex; the vault is the truth").

### Tables

```sql
sources(id, hash, kind, ext, path, url, title, original_name, bytes, pages,
        tree_state, steps, url_status, url_checked_at, bookmark, imported_at, meta)
source_nodes(source_id, node_id, parent_id, depth, ord, title, summary,
             start_page, end_page, text_hash)
atoms(id, topic, path, claim, body, frontmatter, type, status, body_hash,
      size, mtime, created_at, updated_at, indexed_at)
atom_links(from_id, raw_target, to_id, alias, heading, occurrences, position)
atom_sources(atom_id, source_id, node_id, ocr)
clusters(id, signature, created_at, updated_at)
cluster_atoms(cluster_id, atom_id)
runs(id, op, step, model, target_id, status, prompt_tokens, completion_tokens,
     total_tokens, iterations, answer, question, error, started_at, ended_at)
run_steps(run_id, seq, tool, args_digest, result_digest, is_error, duration_ms)
distill_parts(source_id, node_id, state, reason, atoms, run_id, updated_at)
lint_findings(id, run_id, layer, kind, severity, subject_kind, subject_id, message, created_at)
atoms_fts        -- external content over atoms(claim, body)
source_nodes_fts -- external content over source_nodes(title, summary)
```

Notes that matter:

- **`source_nodes.text_hash`** is non-empty exactly for a node that carries text: one such node is
  one **part**, so this column decides "how many parts does this source have".
- **`sources.tags`** and **`sources.auto_tags`** are JSON arrays mirroring the human and derived
  labels in the source's `.meta` record. The record is the truth; `reindex` restores the columns from
  it, and the source-tags vocabulary reads them instead of opening one file per source.
  `auto_tags_at` (NULL = never tagged) is the backfill marker: a completely distilled source without
  it is owed one derivation pass, and an empty answer still stamps it so the pass does not repeat.
- **`runs` is append-only audit.** `op` names the class of work, `step` names which of the four steps
  a pipeline run executed. `tag` is a wiki-page tagging run, `summary` a source's overview run.
  Nothing rebuilds it.
- **`distill_parts` is a record, not a cache.** "Has claims" is not "finished": a run can stop
  mid-part, and a resumed run must redo such a part, never skip it. The same records are mirrored in
  the source's `.meta`.
- **FTS5 is external-content**; triggers (created and rebuilt by `persistence.EnsureFTS`) maintain it.
  If the bundled SQLite lacks FTS5, `status` shows `search: degraded` and search falls back to
  `LIKE`.
- **The page layer is gone:** no `notes`, `note_tags`, `note_links`, `note_atoms`,
  `note_successors`, `notes_fts`. There is no tag table and no page address. Wiki pages are files,
  and clusters have no prose.

### PRAGMAs

Set per connection in the DSN: `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`,
`synchronous=NORMAL`. The pool is small (4), not one connection: WAL lets readers and the writer work
at the same time, and a single connection let one slow aggregate stall every write. An in-memory
index keeps **one** connection — every connection to `:memory:` is a separate database.

## 4. Derived values (never stored, always computed)

- `clusters_of(atom) = cluster_atoms WHERE atom_id = atom.id`
- `atoms_of(cluster) = cluster_atoms WHERE cluster_id = cluster.id`
- `backlinks(x)`, `unresolved(x)`, `outbound(x)` for the atom layer
- `topic_counts` (claim topics, with claim counts)
- `unclustered = atoms with no cluster_atoms row` — the backlog of step 4
- `graph` = atom graph + cluster membership + provenance edges (source node → atom)
- `atom_count` of a source counts **resolvable** claims, never provenance rows alone
- the fourth light of a source is derived from its claims and their memberships, never stored

## 5. Reindex

`reindex` rebuilds the index from the vault:

- **sources** from the content-addressed store, with identity from `<hash>.meta` (kind, URL,
  bookmark, original name, title, language, origin, wiki page, sitemap parent, sitemap marker, feed
  marker, from-feed copy marker, attachment: `attached`/`attached_from`/`attached_hash`);
- **atoms** from their Markdown files; a claim whose every source is gone is trashed, a partial
  provenance is pruned and the file rewritten;
- **clusters** from their records; a member that no longer exists is dropped, an empty record is
  removed, and a row without a record is deleted;
- **`source_nodes`** from the `.outline` sidecars; **`distill_parts`** from the `.meta` records;
  **FTS** from the content.

It never decides from the index alone: the vault is the truth and the index is the cache. The
append-only audit (`runs`, `run_steps`, `lint_findings`) is not touched.
