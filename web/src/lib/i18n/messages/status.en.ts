/** The Status lens: the vault report, the job queue and the maintenance actions. */
export const statusEn: Record<string, string> = {
  // The page head.
  'status.sub': 'The vault, the index and the work in flight.',
  'status.subLine': 'Thought Keeper {version} · up {uptime} · {addr}',
  'status.notLoaded': 'Nothing loaded',
  'status.notLoadedText':
    'Thought Keeper is not reachable. Start the app and this page reconnects by itself.',
  'status.indexShape': 'The index has another shape.',

  // The counters.
  'status.statSources': 'sources',
  'status.statClaims': 'claims',
  'status.statClusters': 'clusters',
  'status.statUnclustered': 'unclustered',

  // The capability chips.
  'status.capAgent': 'Agent',
  'status.capVault': 'Vault',
  'status.capSearch': 'Search',
  'status.capInbox': 'Inbox',
  'status.ready': 'ready',
  'status.missing': 'missing',
  'status.notConfigured': 'not configured',
  'status.writable': 'writable',
  'status.readOnly': 'read-only',
  'status.polled': 'polled',
  'status.watched': 'watched',
  'status.polledReason': 'Polled instead of watched — {error}',

  // Live progress and the job queue.
  'status.waiting': 'waiting',
  'status.liveParts.one': ' · {done}/{total} part',
  'status.liveParts.many': ' · {done}/{total} parts',
  'status.jobQueue': 'Job queue',
  'status.noJobs': 'Nothing is running — no document step, no cluster pass, no ask.',
  'status.jobOwed': 'owed',
  'status.wholeVault': 'over the whole vault',
  'status.moreQueued.one': '{count} more queued — it runs as the workers reach it',
  'status.moreQueued.many': '{count} more queued — they run as the workers reach them',

  // Summaries.
  'status.summaries': 'Summaries',
  'status.checkedAt': 'checked {time}',
  'status.summaryQueued': 'queued',
  'status.summaryRunning': 'running',
  'status.summaryNote':
    "A source's overview is written after a complete distill. The consistency check measures the missing ones; the summary worker fills them in a few at a time, so this drains in the background.",

  // Clusters.
  'status.clusters': 'Clusters',
  'status.clusterClaims': 'Cluster claims',
  'status.clusterClaimsHint':
    'Step 4 by hand: group the claims no cluster owns yet. It waits until no document step is running.',

  // Vocabulary.
  'status.vocabulary': 'Vocabulary',
  'status.curate': 'Curate',
  'status.retopic': 'Re-topic claims',
  'status.vocabNote':
    'asks the model what the names should become — claim topics, wiki tags and source tags — and applies the answer as a rename. A claim keeps its text, its node and its associations; a page and a source keep their prose. Only the label changes. Topics are flat.',
  'status.vocabNames.one': '{count} name',
  'status.vocabNames.many': '{count} names',
  'status.vocabUses.one': '{count} use',
  'status.vocabUses.many': '{count} uses',
  'status.vocabSingletons': '{count} used once',
  'status.vocabTop': 'top {pct}%',
  'status.vocabCanonical': '{canonical} canonical, {absorbed} absorbed',
  'status.vocabNew': '{count} new',
  'status.collisionsTitle': 'Two spellings of one name — always a bug',
  'status.collisions.one': '{count} collision',
  'status.collisions.many': '{count} collisions',
  'status.familiesTitle': 'Names that share their words — a question worth asking',
  'status.families.one': '{count} shared-word family',
  'status.families.many': '{count} shared-word families',
  'status.broadTokensTitle': 'Learned from this vault: ignored by near-duplicate detection',
  'status.broadTokens.one': '{count} broad token',
  'status.broadTokens.many': '{count} broad tokens',
  'status.broadFamiliesTitle': 'Too broad to merge: counted, never suggested',
  'status.broadFamilies.one': '{count} broad family ignored',
  'status.broadFamilies.many': '{count} broad families ignored',
  'status.noVocabulary': 'No vocabulary report yet.',

  // The vault section.
  'status.vault': 'Vault',
  'status.groupCount.one': '{count} group',
  'status.groupCount.many': '{count} groups',
  'status.duplicates.one': '{count} claim file in {groups} exists more than once',
  'status.duplicates.many': '{count} claim files in {groups} exist more than once',
  'status.duplicatesExample': ' — e.g. “{example}”',
  'status.duplicatesNote': 'The copies are indistinguishable; collapsing keeps one.',

  // The maintenance actions.
  'status.reconcile': 'Check consistency',
  'status.reconcileHint':
    'Walk the vault and verify that every step record matches what is on disk, and that the index mirrors the records — a source that lost its owner is restored from its record (no source is derived again). A missing outline invalidates its derivation.',
  'status.reindex': 'Reindex',
  'status.reindexHint':
    'Rebuild the index from the vault: sources, claims and clusters; drop rows whose file is gone.',
  'status.rebuildOutlines': 'Rebuild outlines',
  'status.rebuildOutlinesHint':
    'Rebuild every source’s outline (step 2) from its capture — no extraction, no model.',
  'status.refreshUrls': 'Refresh URL sources',
  'status.refreshUrlsHint':
    'Re-fetch every known URL once. A changed page is corrected through its existing source.',
  'status.collapseDuplicates': 'Collapse duplicate claims',
  'status.collapseDuplicatesHint':
    'Keep one copy of every claim that exists more than once and move the indistinguishable copies to the trash.',

  // Vault details.
  'status.vaultDetails': 'Vault details',
  'status.kvVault': 'vault',
  'status.kvIndex': 'index',
  'status.kvSources': 'sources',
  'status.kvInbox': 'inbox',
  'status.kvLanguageUI': 'interface language',
  'status.kvLanguageClaims': 'claims language',
  'status.kvLanguageSummary': 'summary language',

  // A failed run and the maintenance notices. The server's own text stays; only the prefix is ours.
  'status.run': 'run',
  'status.runFailed': '{op} failed{reason}',
  'status.clusterQueued': 'Cluster pass queued — it waits until no document step is running.',
  'status.clusterNothing': 'Nothing to cluster — every claim is associated.',
  'status.clusterFailed': 'Cluster failed: {error}',
  'status.reconcileNotice': 'Checking the vault…',
  'status.checked': 'checked {count}',
  'status.invalidated': '{count} invalidated',
  'status.broken': '{count} broken',
  'status.languagesDetected.one': '{count} language detected',
  'status.languagesDetected.many': '{count} languages detected',
  'status.identities.one': '{count} identity restored',
  'status.identities.many': '{count} identities restored',
  'status.owners.one': '{count} owner restored',
  'status.owners.many': '{count} owners restored',
  'status.consistency': 'Consistency: {bits}.',
  'status.reconcileFailed': 'Consistency check failed: {error}',
  'status.reindexNotice': 'Rebuilding the index from the vault…',
  'status.claims.one': '{count} claim',
  'status.claims.many': '{count} claims',
  'status.clusterCount.one': '{count} cluster',
  'status.clusterCount.many': '{count} clusters',
  'status.skipped': '{count} skipped',
  'status.orphanRows.one': '{count} orphan row removed',
  'status.orphanRows.many': '{count} orphan rows removed',
  'status.trashed.one': '{count} claim trashed',
  'status.trashed.many': '{count} claims trashed',
  'status.deadSource.one': '{count} claim lost a dead source',
  'status.deadSource.many': '{count} claims lost a dead source',
  'status.emptyClusters.one': '{count} empty cluster removed',
  'status.emptyClusters.many': '{count} empty clusters removed',
  'status.reindexDone': 'Reindex complete: {bits}.',
  'status.reindexFailed': 'Reindex failed: {error}',
  'status.outlinesNotice': 'Rebuilding every outline…',
  'status.outlinesRebuilt': 'Outlines rebuilt: {built}/{sources}{failed}.',
  'status.failedCount': ' · {count} failed',
  'status.outlinesFailed': 'Outline rebuild failed: {error}',
  'status.curateStarted': 'Curating the claim-topic vocabulary…',
  'status.curateFailed': 'Vocabulary curation failed: {error}',
  'status.retopicStarted': 'Re-deriving claim topics from the claim texts…',
  'status.retopicFailed': 'Topic re-derivation failed: {error}',
  'status.collapseNothing': 'Nothing to collapse — every claim exists once.',
  'status.collapseTitle.one': 'Collapse {count} duplicate group?',
  'status.collapseTitle.many': 'Collapse {count} duplicate groups?',
  'status.collapseMessage':
    'One copy of every duplicate claim is kept; the indistinguishable copies move to .trash/atoms/. Nothing is rewritten.',
  'status.collapseConfirm': 'Collapse duplicates',
  'status.collapseKept': 'Kept one copy in {groups}, moved {files} to the trash.',
  'status.files.one': '{count} file',
  'status.files.many': '{count} files',
  'status.collapseFailed': 'Collapse failed: {error}',
};
