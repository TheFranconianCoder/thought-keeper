/**
 * Shared words and sentences: the pieces every lens uses, plus the application chrome's status
 * labels. One key per thing, so a lens never invents its own word for a shared idea.
 */
export const commonEn: Record<string, string> = {
  // The six lenses and the two shelves.
  'view.home': 'Start',
  'view.claims': 'Claims',
  'view.wiki': 'Wiki',
  'view.sources': 'Sources',
  'view.source': 'Sources',
  'view.attention': 'Attention',
  'view.bookmarks': 'Bookmarks',
  'view.ask': 'Ask',
  'view.status': 'Status',
  'nav.library': 'Library',
  'nav.work': 'Work',
  'nav.primary': 'Main navigation',
  'nav.search': 'Search…',
  'nav.searchTitle': 'Search claims and sources (Ctrl/Cmd+K)',
  'nav.addSource': 'Add source',

  'common.cancel': 'Cancel',
  'common.confirm': 'Confirm',
  'common.delete': 'Delete',
  'common.close': 'Close',
  'common.dismiss': 'Dismiss',
  'common.retry': 'Try again',
  'common.clear': 'clear',
  'common.clearField': 'Clear field',
  'common.loading': 'Loading…',
  'common.none': '—',

  'connection.title': 'The UI updates through one event stream',
  'connection.live': 'Online',
  'connection.offline': 'Offline',
  'connection.reconnecting': 'Reconnecting',
  'connection.connecting': 'Connecting',

  'theme.titleAuto': 'Theme: follow the system',
  'theme.titleLight': 'Theme: light',
  'theme.titleDark': 'Theme: dark',

  'language.title': 'Interface language: {language}',

  'app.offlineTitle': 'This device is offline.',
  'app.offlineBody':
    'The app may be stopped, or the connection dropped. What you see may be out of date; the page reconnects by itself.',
  'app.notConnectedTitle': 'Not connected to Thought Keeper.',
  'app.documentTitle': '{title} · Thought Keeper',
  'app.brand': 'Thought Keeper',

  'duplicate.title': 'Thought Keeper is already open',
  'duplicate.description':
    'Only one window of the app is supported. Another window already holds the primary view.',
  'duplicate.useThis': 'Use this window',
  'duplicate.focusOther': 'Focus the other window',

  'copy.copied': '{label} copied.',
  'copy.failed': 'Could not copy the {label}.',
  'copy.title': 'Copy {label}',

  'markdown.reference': 'Reference {number}',
  'markdown.backToReference': 'Back to reference {number}',

  'format.justNow': 'just now',
  'format.minutesAgo': '{count}m ago',
  'format.hoursAgo': '{count}h ago',
  'format.daysAgo.one': '{count}d ago',
  'format.daysAgo.many': '{count}d ago',

  'errors.cancelled': 'The request was cancelled.',
};
