/** The Start lens: the front door, not a database dump. Ask, see what runs, see what arrived. */
export const homeEn: Record<string, string> = {
  'home.title': 'Start',

  'home.ask': 'Ask the vault',
  'home.askPlaceholder': 'What do the claims say about…',
  'home.askButton': 'Ask',
  'home.askNote': 'Every answer is saved with its citations.',
  'home.recentQuestions': 'Recently asked',

  // The two signal lines under the box: what runs, what waits. Both are absent when quiet.
  'home.working': 'Working: {what}',
  'home.needsYou': 'Needs you',
  'home.needsFailed.one': '{count} source with a failed step',
  'home.needsFailed.many': '{count} sources with a failed step',
  'home.needsFindings.one': '{count} finding',
  'home.needsFindings.many': '{count} findings',
  'home.needsUnclustered.one': '{count} claim without a cluster',
  'home.needsUnclustered.many': '{count} claims without a cluster',

  'home.newSources': 'Recently arrived',
  'home.newSourcesEmpty': 'Nothing has arrived yet.',
  'home.allSources': 'All sources',
  'home.claimsCount.one': '{count} claim',
  'home.claimsCount.many': '{count} claims',
  'home.emptySourcesTitle': 'No sources yet',
  'home.emptySourcesText':
    'Add a Markdown file, a PDF, an EPUB, plain text or a link — or ask a question; the vault can answer that it is still empty.',
  'home.emptyInbox': 'Files in the inbox/ folder are picked up automatically.',
  'home.emptySourcesAction': 'Add the first source',

  'home.pulse.sources.one': '{count} source',
  'home.pulse.sources.many': '{count} sources',
  'home.pulse.claims.one': '{count} claim',
  'home.pulse.claims.many': '{count} claims',
  'home.pulse.clusters.one': '{count} cluster',
  'home.pulse.clusters.many': '{count} clusters',

  // The read-later queue on the front door.
  'home.readLater': 'Read later',
  'home.allReadLater': 'All',
  'home.readLaterEmpty': 'Nothing waiting.',
  'home.readLaterEmptyAction': 'Set up interests',
  'home.readLaterDone': 'Done',
};
