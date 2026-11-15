/** Die Linse „Start“: die Haustür, keine Datenbankausgabe. Fragen, laufende Arbeit, was geschah. */
export const homeDe: Record<string, string> = {
  'home.title': 'Start',

  'home.ask': 'Den Wissensbestand fragen',
  'home.askPlaceholder': 'Was sagen die Aussagen über …',
  'home.askButton': 'Fragen',
  'home.askNote': 'Jede Antwort wird mit ihren Belegen gespeichert.',
  'home.recentQuestions': 'Zuletzt gefragt',

  // Die zwei Signalzeilen unter der Frage: was läuft, was wartet. Beide fehlen, wenn nichts ist.
  'home.working': 'Läuft: {what}',
  'home.needsYou': 'Braucht dich',
  'home.needsFailed.one': '{count} Quelle mit fehlgeschlagenem Schritt',
  'home.needsFailed.many': '{count} Quellen mit fehlgeschlagenem Schritt',
  'home.needsFindings.one': '{count} Befund',
  'home.needsFindings.many': '{count} Befunde',
  'home.needsUnclustered.one': '{count} Aussage ohne Cluster',
  'home.needsUnclustered.many': '{count} Aussagen ohne Cluster',

  // Die Aktivitätsstatistik: was seit dem LETZTEN START-BESUCH passiert ist. Sie ist unabhängig
  // von den Sources-Markierungen, die ihr eigenes Wasserzeichen behalten, bis die Liste geöffnet wird.
  'home.sinceLastVisit': 'Seit deinem letzten Besuch',
  'home.lastVisit': 'letzter Besuch {age}',
  'home.newSources.one': '{count} neue Quelle',
  'home.newSources.many': '{count} neue Quellen',
  'home.changedSources.one': '{count} aktualisierte Quelle',
  'home.changedSources.many': '{count} aktualisierte Quellen',
  'home.moreSources': '+{count} weitere',
  'home.openSources': 'Zu den Quellen',
  'home.emptySourcesTitle': 'Noch keine Quelle',
  'home.emptySourcesText':
    'Füge eine Markdown-Datei, ein PDF, ein EPUB, reinen Text oder einen Link hinzu — oder stelle eine Frage; der Tresor darf antworten, dass er noch leer ist.',
  'home.emptyInbox': 'Dateien im Ordner inbox/ werden automatisch übernommen.',
  'home.emptySourcesAction': 'Erste Quelle hinzufügen',

  'home.pulse.sources.one': '{count} Quelle',
  'home.pulse.sources.many': '{count} Quellen',
  'home.pulse.claims.one': '{count} Aussage',
  'home.pulse.claims.many': '{count} Aussagen',
  'home.pulse.clusters.one': '{count} Cluster',
  'home.pulse.clusters.many': '{count} Cluster',

  // Die Warteschlange auf der Haustür.
  'home.readLater': 'Später lesen',
  'home.allReadLater': 'Alle',
  'home.readLaterOpen.one': '{count} offen',
  'home.readLaterOpen.many': '{count} offen',
  'home.readLaterOldest': 'ältester {age}',
  'home.readLaterEmptyAction': 'Interessen anlegen',
};
