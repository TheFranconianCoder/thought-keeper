/** Die Linse „Start“: die Haustür, keine Datenbankausgabe. Fragen, laufende Arbeit, Ankunft. */
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

  'home.arrivals': 'Angekommen',
  'home.arrivalsWindow': 'letzte 3 Tage',
  'home.arrivalsCount.one': '{count} neu',
  'home.arrivalsCount.many': '{count} neu',
  'home.arrivalInterests': 'Aus deinen Interessen:',
  'home.allSources': 'Alle Quellen',
  'home.arrivalAge': 'kam {age}',
  'home.claimsCount.one': '{count} Aussage',
  'home.claimsCount.many': '{count} Aussagen',
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
