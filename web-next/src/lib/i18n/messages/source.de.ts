/** Eine Quelle: ihre Tabs, Schritte, Herkunft und Aktionen. */
export const sourceDe: Record<string, string> = {
  // Kopfzeile und Aktionen.
  'source.backToSources': 'Zurück zu den Quellen',
  'source.openUrl': 'URL öffnen',
  'source.refresh': 'Aktualisieren',
  'source.capture': 'Erfassen',
  'source.outline': 'Gliederung',
  'source.distill': 'Destillieren',
  'source.reset': 'Zurücksetzen',
  'source.feed': 'Feed',
  'source.sitemap': 'Sitemap',
  'source.pages.one': '{count} Seite',
  'source.pages.many': '{count} Seiten',

  // Tabs.
  'source.viewAria': 'Quellenansicht',
  'source.tabSnapshot': 'Momentaufnahme',
  'source.tabLive': 'Live',
  'source.tabText': 'Text',
  'source.tabContent': 'Inhalt',
  'source.tabSummary': 'Zusammenfassung',
  'source.tabLog': 'Protokoll',
  'source.tabFiles': 'Dateien',
  'source.tabChildren': 'Unterquellen',

  // Tab „Zusammenfassung“.
  'source.summaryIntro':
    'Eine eigenständige Übersicht dieser Quelle, aus ihren Aussagen in der eingestellten Sprache der Zusammenfassung geschrieben. Sie ist selbst keine Aussage: die Aussagen unten bleiben die Quelle der Wahrheit.',
  'source.rewriteTitle':
    'Die Übersicht erneut aus den aktuellen Aussagen schreiben (in der eingestellten Sprache)',
  'source.rewriting': 'Wird neu geschrieben…',
  'source.rewrite': 'Zusammenfassung neu schreiben',
  'source.loadingSummary': 'Die Zusammenfassung wird geladen…',
  'source.summaryEmpty':
    'Diese Quelle hat noch keine Zusammenfassung. Die Zusammenfassung wird direkt nach dem Schritt Destillieren geschrieben; der Konsistenz-Auftrag ergänzt die fehlenden.',
  'source.generating': 'Wird erzeugt…',
  'source.generateSummary': 'Zusammenfassung erzeugen',
  'source.summaryNeedsDistill':
    'Schließe zuerst die Ableitung ab: eine Zusammenfassung einer Quelle, deren Aussagen noch geschrieben werden, würde ein bewegliches Ziel beschreiben.',

  // Tab „Unterquellen“.
  'source.childrenEmpty': 'Diese Quelle besitzt noch keine Unterquellen.',
  'source.childrenIntro':
    'Die Quellen, die diese in den Tresor gebracht hat. Sie gehören zu ihr: wer sie entfernt, entfernt auch jene.',

  // Tab „Dateien“.
  'source.backToFiles': 'Zurück zu den Dateien',
  'source.openPdf': 'Das PDF öffnen',
  'source.pdfNotText': 'Ein PDF ist kein Text — öffne es in seinem eigenen Tab.',
  'source.fileNotListed': 'Diese Datei steht für die Quelle nicht mehr in der Liste.',
  'source.fileShowingFirst': 'Die ersten {size} von {total} werden angezeigt.',
  'source.filesIntroStart':
    'Das Objekt und jede abgeleitete Datei dieser Quelle. Wähle eine zum Lesen. Eine Größe von',
  'source.filesIntroEnd': 'bedeutet, dass die Datei fehlt; der Konsistenz-Auftrag merkt das.',
  'source.missing': 'fehlt',

  // Tabs „Protokoll“, „Live“ und PDF.
  'source.logShowingLast': 'Die letzten {size} von {total} werden angezeigt.',
  'source.noLog': 'Keine Protokolleinträge.',
  'source.liveTitle': 'Live-Seite',
  'source.loadingPdf': 'Das PDF wird geladen…',

  // Ein Teil und die Gliederung.
  'source.backToOutline': 'Zurück zur Gliederung',
  'source.partGone': 'Dieser Teil steht nicht mehr in der Gliederung der Quelle: {error}',
  'source.showWhole': 'Die ganze Quelle zeigen',
  'source.partTooBig': 'Dieser Teil allein hat {size} — die ersten {shown} werden angezeigt.',
  'source.tooBig': '{size} — zu viel für eine Seite.',
  'source.readableParts.one': 'Ihre Gliederung hat {count} lesbaren Teil; wähle einen.',
  'source.readableParts.many': 'Ihre Gliederung hat {count} lesbare Teile; wähle einen.',
  'source.untitled': '(ohne Titel)',
  'source.noTextYet':
    'Noch kein lesbarer Text — diese Quelle wurde nicht erfasst, oder die Extraktion hat nichts ergeben.',
  'source.noOutlineYet':
    'Die ersten {size} von {total} werden angezeigt — diese Quelle hat noch keine Gliederung. Baue die Gliederung, dann wird sie Teil für Teil lesbar.',

  // Die Seitenleiste.
  'source.detailsAria': 'Quellendetails',
  'source.steps': 'Schritte',
  'source.snapshot': 'Momentaufnahme',
  'source.attachedHere':
    'Diese Momentaufnahme wurde von Hand angehängt. Die wöchentliche Aktualisierung lässt sie unberührt. Ersetze sie durch eine andere Datei oder frage die Website erneut — die Seite ersetzt sie nur, wenn sie lesbar ist; eine Wand behält sie.',
  'source.attachedFrom':
    'Diese Momentaufnahme wurde von Hand aus {from} angehängt. Die wöchentliche Aktualisierung lässt sie unberührt. Ersetze sie durch eine andere Datei oder frage die Website erneut — die Seite ersetzt sie nur, wenn sie lesbar ist; eine Wand behält sie.',
  'source.bookmarkTextStart':
    'Diese Seite hat keinen lesbaren Text geliefert (eine JavaScript-Seite, ein Video oder eine App). Öffne sie im Browser,',
  'source.bookmarkPrint': 'drucke sie als PDF',
  'source.bookmarkTextEnd':
    '(oder kopiere den Text) und hänge sie hier an. Die URL bleibt die Adresse dieser Quelle.',
  'source.blocked':
    'Diese Seite konnte nicht gelesen werden. Öffne sie im Browser, speichere sie als HTML oder PDF (oder kopiere den Text) und hänge sie hier an. Die URL bleibt die Adresse dieser Quelle.',
  'source.blockedWithDetail':
    'Diese Seite konnte nicht gelesen werden — {detail}. Öffne sie im Browser, speichere sie als HTML oder PDF (oder kopiere den Text) und hänge sie hier an. Die URL bleibt die Adresse dieser Quelle.',
  'source.attaching': 'Wird angehängt…',
  'source.replaceSnapshot': 'Momentaufnahme ersetzen…',
  'source.attachSnapshot': 'Momentaufnahme anhängen…',
  'source.fetching': 'Wird abgerufen…',
  'source.tryUrlAgain': 'Die URL erneut versuchen',

  // Block „Details“.
  'source.details': 'Details',
  'source.kindLabel': 'Art',
  'source.sizeLabel': 'Größe',
  'source.languageLabel': 'Sprache',
  'source.languageHint': 'Erkannte Sprachangabe',
  'source.languageBookmarkHint':
    'Ein Lesezeichen hat keinen lesbaren Text, daher wird keine Sprache erkannt.',
  'source.languageUnknownHint':
    'Für diese Quelle wurde keine Sprache erkannt. Erfasse sie erneut oder führe unter Status „Konsistenz prüfen“ aus.',
  'source.notIndexed': 'nicht indiziert',
  'source.unknown': 'unbekannt',
  'source.pagesLabel': 'Seiten',
  'source.outlineLabel': 'Gliederung',
  'source.addedLabel': 'hinzugefügt',
  'source.urlLabel': 'URL',
  'source.httpLabel': 'HTTP',
  'source.idLabel': 'Quellen-ID',
  'source.partOfFeed': 'Teil eines Feeds',
  'source.partOfSitemap': 'Teil einer Sitemap',
  'source.copyLabel': 'Kopie',
  'source.sitemapOwns': 'besitzt die Seiten, die sie auflistet',
  'source.feedOwns': 'besitzt die Seiten, auf die ihre Einträge zeigen',
  'source.copyFromFeed': 'aus ihrem Feed übernommen — die Seite selbst ist nicht lesbar',
  'source.openPage': 'die Seite öffnen',
  'source.linkedFromPage': 'von einer Seite verlinkt',
  'source.ownedBy': 'Gehört zu {owner} — entferne den Besitzer, um diese Quelle zu entfernen.',
  'source.ownerSitemap': 'seiner Sitemap',
  'source.ownerFeed': 'seinem Feed',
  'source.ownerWiki': 'seiner Wiki-Seite',

  // Schlagwörter.
  'source.tags': 'Schlagwörter',
  'source.edit': 'bearbeiten',
  'source.tagsAria': 'Schlagwörter der Quelle',
  'source.saving': 'Wird gespeichert…',
  'source.save': 'Speichern',
  'source.noTags': 'Noch keine Schlagwörter — ordne diese Quelle ein paar groben Etiketten zu.',
  'source.autoTags': 'Automatische Schlagwörter',
  'source.autoTagsTitle': 'Aus den Aussagen dieser Quelle abgeleitet',
  'source.autoTagsNote':
    'Abgeleitet aus den Aussagen nach einem vollständigen Destillieren; der Agent schreibt sie, du nicht.',

  // Themen und Fortschritt.
  'source.topics': 'Themen',
  'source.topicClaims.one': '{count} Aussage',
  'source.topicClaims.many': '{count} Aussagen',
  'source.moreTopics.one': '+{count} weiteres Thema',
  'source.moreTopics.many': '+{count} weitere Themen',
  'source.progress': 'Fortschritt',
  'source.stateLabel': 'Zustand',
  'source.nowLabel': 'jetzt',
  'source.partsLabel': 'Teile',
  'source.claimsLabel': 'Aussagen',
};
