/** Aufmerksamkeit und Lesezeichen: die Problemliste und die gesammelten Links. */
export const attentionDe: Record<string, string> = {
  // Aufmerksamkeit: der Seitenkopf.
  'attention.loading': 'Wird geladen, was Aufmerksamkeit braucht …',
  'attention.none': 'Nichts braucht eine Entscheidung oder eine Reparatur.',
  'attention.count.one': '{count} Sache braucht eine Entscheidung oder eine Reparatur.',
  'attention.count.many': '{count} Sachen brauchen eine Entscheidung oder eine Reparatur.',
  'attention.vaultStatus': 'Tresorstatus',

  // Aufmerksamkeit: die beiden Hälften der Liste.
  'attention.unreachableTitle': 'Nichts geladen',
  'attention.unreachableText':
    'Thought Keeper ist nicht erreichbar; das ist also keine Aussage über den Tresor.',
  'attention.failedSteps': 'Fehlgeschlagene Quellenschritte',
  'attention.failedChip': 'fehlgeschlagen',
  'attention.lintFindings': 'Lint-Befunde',
  'attention.open': '{kind} öffnen',
  'attention.infoCount.one': '{count} informativer Befund',
  'attention.infoCount.many': '{count} informative Befunde',
  'attention.emptyTitle': 'Nichts braucht Aufmerksamkeit',
  'attention.emptyText': 'Quellenableitungen und Lint-Befunde sind in Ordnung.',

  // Aufmerksamkeit: Hinweise. Der Text des Servers bleibt, nur das Präfix ist unseres.
  'attention.sourcesFailed': 'Quellen konnten nicht geladen werden: {error}',
  'attention.findingsFailed': 'Befunde konnten nicht geladen werden: {error}',

  // Lesezeichen: der Seitenkopf und der Filter.
  'bookmarks.sub':
    'Gesammelte Links, die nur als Metadaten existieren — nicht indexiert, aber deine.',
  'bookmarks.add': 'Link hinzufügen',
  'bookmarks.addEmpty': 'Link hinzufügen',
  'bookmarks.filterPlaceholder': 'Lesezeichen filtern …',
  'bookmarks.filterAria': 'Lesezeichen filtern',
  'bookmarks.count.one': '{count} Lesezeichen',
  'bookmarks.count.many': '{count} Lesezeichen',
  'bookmarks.otherHost': 'andere',

  // Lesezeichen: die leeren Zustände und der Fehler.
  'bookmarks.unreachableTitle': 'Nichts geladen',
  'bookmarks.unreachableText':
    'Thought Keeper ist nicht erreichbar; das ist also nicht die Antwort des Tresors.',
  'bookmarks.emptyTitle': 'Noch keine Lesezeichen',
  'bookmarks.emptyText':
    'Füge einen beliebigen Link ein. Seiten, die nicht gelesen werden können — Video, Paywall, App —, werden hier gesammelt statt still verworfen.',
  'bookmarks.noMatch': 'Kein Lesezeichen passt',
  'bookmarks.noMatchText': 'Entferne den Filter, um alle gesammelten Links zu sehen.',
  'bookmarks.loadFailed': 'Lesezeichen konnten nicht geladen werden: {error}',

  // Lesezeichen: eine Zeile.
  'bookmarks.checked': 'geprüft {time}',
  'bookmarks.openExternal': 'Link im Browser öffnen',
};
