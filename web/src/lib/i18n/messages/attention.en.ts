/** Attention and Bookmarks: the trouble list and the saved links. */
export const attentionEn: Record<string, string> = {
  // Attention: the page head and its search.
  'attention.vaultStatus': 'Vault status',
  'attention.filterPlaceholder': 'Filter attention…',
  'attention.filterAria': 'Filter what needs attention',
  'attention.noMatchTitle': 'Nothing matches',
  'attention.noMatchText': 'Clear the filter to see everything that needs attention.',

  // Attention: the two halves of the list.
  'attention.unreachableTitle': 'Nothing loaded',
  'attention.unreachableText':
    'Thought Keeper is not reachable, so this is not a statement about the vault.',
  'attention.failedSteps': 'Failed source steps',
  'attention.failedChip': 'failed',
  'attention.lintFindings': 'Lint findings',
  'attention.open': 'Open the {kind}',
  'attention.infoCount.one': '{count} informational finding',
  'attention.infoCount.many': '{count} informational findings',
  'attention.emptyTitle': 'Nothing needs attention',
  'attention.emptyText': 'Source derivations and lint findings are in order.',

  // Attention: notices. The server's own words are kept, only the prefix is ours.
  'attention.sourcesFailed': 'Could not load sources: {error}',
  'attention.findingsFailed': 'Could not load findings: {error}',

  // Bookmarks: the page head and the filter.
  'bookmarks.add': 'Add link',
  'bookmarks.addEmpty': 'Add a link',
  'bookmarks.filterPlaceholder': 'Filter bookmarks…',
  'bookmarks.filterAria': 'Filter bookmarks',
  'bookmarks.otherHost': 'other',

  // Bookmarks: the empty and error states.
  'bookmarks.unreachableTitle': 'Nothing loaded',
  'bookmarks.unreachableText': "Thought Keeper is not reachable, so this is not the vault's answer.",
  'bookmarks.emptyTitle': 'No bookmarks yet',
  'bookmarks.emptyText':
    'Paste any link. Pages that cannot be read — video, paywall, app — are collected here instead of being silently dropped.',
  'bookmarks.noMatch': 'No bookmark matches',
  'bookmarks.noMatchText': 'Clear the filter to see every collected link.',
  'bookmarks.loadFailed': 'Could not load bookmarks: {error}',

  // Bookmarks: one row.
  'bookmarks.checked': 'checked {time}',
  'bookmarks.openExternal': 'Open the link in the browser',
};
