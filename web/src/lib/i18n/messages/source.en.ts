/** One source: its tabs, steps, provenance and actions. */
export const sourceEn: Record<string, string> = {
  // Header and actions.
  'source.backToSources': 'Back to sources',
  'source.ask': 'Ask',
  'source.openUrl': 'Open URL',
  'source.refresh': 'Refresh',
  'source.refreshPage': 'Fetch the page again',
  'source.tryPage': 'Try the page again',
  'source.capture': 'Capture',
  'source.outline': 'Outline',
  'source.distill': 'Distill',
  'source.reset': 'Reset',
  'source.feed': 'feed',
  'source.sitemap': 'sitemap',
  'source.pages.one': '{count} page',
  'source.pages.many': '{count} pages',
  'source.chapters.one': '{count} chapter',
  'source.chapters.many': '{count} chapters',

  // Tabs.
  'source.viewAria': 'Source view',
  'source.tabSnapshot': 'Snapshot',
  'source.tabLive': 'Live',
  'source.tabText': 'Text',
  'source.tabContent': 'Content',
  'source.tabSummary': 'Summary',
  'source.tabLog': 'Log',
  'source.tabFiles': 'Files',
  'source.tabChildren': 'Sub-sources',

  // Summary tab.
  'source.summaryIntro':
    'A standalone overview of this source, written from its claims in the configured summary language. It is not a claim itself: the claims below stay the source of truth.',
  'source.rewriteTitle':
    'Write the overview again from the current claims (in the configured summary language)',
  'source.rewriting': 'Rewriting…',
  'source.rewrite': 'Rewrite summary',
  'source.loadingSummary': 'Loading the summary…',
  'source.summaryEmpty':
    'This source has no summary yet. The summary is written right after the distill step; the consistency job fills in the ones that are missing.',
  'source.generating': 'Generating…',
  'source.generateSummary': 'Generate summary',
  'source.summaryNeedsDistill':
    'Finish the derivation first: a summary of a source whose claims are still being written would describe a moving target.',

  // Sub-sources tab.
  'source.childrenEmpty': 'This source owns no sub-sources yet.',
  'source.childrenIntro':
    'The sources this one brought into the vault. They are owned by it: removing it removes them too.',

  // Files tab.
  'source.backToFiles': 'Back to files',
  'source.openPdf': 'Open the PDF',
  'source.pdfNotText': 'A PDF is not text — open it in its own tab.',
  'source.epubNotText': 'An EPUB is a container, not text — its text is the captured rendering in the Text tab.',
  'source.fileNotListed': 'This file is no longer listed for the source.',
  'source.fileShowingFirst': 'Showing the first {size} of {total}.',
  'source.filesIntroStart':
    'The object and every derived file of this source. Pick one to read it. A size of',
  'source.filesIntroEnd': 'means the file is missing; the consistency job notices that.',
  'source.missing': 'missing',

  // Log, live and PDF tabs.
  'source.logShowingLast': 'Showing the last {size} of {total}.',
  'source.noLog': 'No log entries.',
  'source.liveTitle': 'Live page',
  'source.loadingPdf': 'Loading the PDF…',

  // Reading a part, and the outline.
  'source.backToOutline': 'Back to the outline',
  'source.partGone': "This part is no longer in the source's outline: {error}",
  'source.showWhole': 'Show the whole source',
  'source.partTooBig': 'This part alone is {size} — showing the first {shown}.',
  'source.tooBig': '{size} — too much for one page.',
  'source.readableParts.one': 'Its outline has {count} readable part; pick one.',
  'source.readableParts.many': 'Its outline has {count} readable parts; pick one.',
  'source.untitled': '(untitled)',
  'source.noTextYet':
    'No readable text yet — this source has not been captured, or extraction produced nothing.',
  'source.noOutlineYet':
    'Showing the first {size} of {total} — this source has no outline yet. Build the outline and it becomes readable part by part.',

  // The rail.
  'source.detailsAria': 'Source details',
  'source.steps': 'Steps',
  'source.snapshot': 'Snapshot',
  'source.attachedHere':
    'This snapshot was attached by hand. The weekly refresh leaves it alone. Replace it with another file, or ask the site again — the page replaces it only when it is readable; a wall keeps it.',
  'source.attachedFrom':
    'This snapshot was attached by hand from {from}. The weekly refresh leaves it alone. Replace it with another file, or ask the site again — the page replaces it only when it is readable; a wall keeps it.',
  'source.bookmarkTextStart':
    'This page gave no readable text (a JavaScript page, a video or an app). Open it in your browser,',
  'source.bookmarkPrint': 'print it to PDF',
  'source.bookmarkTextEnd':
    '(or copy the text), and attach it here. The URL stays the address of this source.',
  'source.blocked':
    'This page could not be read. Open it in your browser, save it as HTML or PDF (or copy the text), and attach it here. The URL stays the address of this source.',
  'source.blockedWithDetail':
    'This page could not be read — {detail}. Open it in your browser, save it as HTML or PDF (or copy the text), and attach it here. The URL stays the address of this source.',
  'source.attaching': 'Attaching…',
  'source.replaceSnapshot': 'Replace snapshot…',
  'source.attachSnapshot': 'Attach snapshot…',
  'source.fetching': 'Fetching…',
  'source.tryUrlAgain': 'Try the URL again',

  // Details block.
  'source.details': 'Details',
  'source.kindLabel': 'kind',
  'source.sizeLabel': 'size',
  'source.languageLabel': 'language',
  'source.languageHint': 'Detected language hint',
  'source.languageBookmarkHint':
    'A bookmark has no readable text, so no language is detected.',
  'source.languageUnknownHint':
    'No language hint has been detected for this source. Capture it again, or run Check consistency in Status.',
  'source.notIndexed': 'not indexed',
  'source.unknown': 'unknown',
  'source.pagesLabel': 'pages',
  'source.chaptersLabel': 'chapters',
  'source.outlineLabel': 'outline',
  'source.addedLabel': 'added',
  'source.urlLabel': 'url',
  'source.httpLabel': 'http',
  'source.idLabel': 'Source ID',
  'source.partOfFeed': 'part of a feed',
  'source.partOfSitemap': 'part of a sitemap',
  'source.copyLabel': 'copy',
  'source.sitemapOwns': 'owns the pages it lists',
  'source.feedOwns': 'owns the pages its items point at',
  'source.copyFromFeed': 'kept from its feed — the page did not replace it',
  'source.openPage': 'open the page',
  'source.linkedFromPage': 'linked from a page',
  'source.ownedBy': 'Owned by {owner} — remove the owner to remove this source.',
  'source.ownerSitemap': 'its sitemap',
  'source.ownerFeed': 'its feed',
  'source.ownerWiki': 'its wiki page',

  // Tags.
  'source.tags': 'Tags',
  'source.edit': 'edit',
  'source.tagsAria': 'Source tags',
  'source.saving': 'Saving…',
  'source.save': 'Save',
  'source.noTags': 'No tags yet — file this source under a few coarse labels.',
  'source.autoTags': 'Auto tags',
  'source.autoTagsTitle': "Derived from this source's claims",
  'source.autoTagsNote':
    "Derived from the claims after a complete distill; the agent writes them, you don't.",

  // Topics and progress.
  'source.topics': 'Topics',
  'source.topicClaims.one': '{count} claim',
  'source.topicClaims.many': '{count} claims',
  'source.moreTopics.one': '+{count} more topic',
  'source.moreTopics.many': '+{count} more topics',
  'source.progress': 'Progress',
  'source.stateLabel': 'state',
  'source.nowLabel': 'now',
  'source.partsLabel': 'parts',
  'source.claimsLabel': 'claims',

  // The reader's marks and the interest block in the rail.
  'source.favorite': 'Favorite',
  'source.unfavorite': 'Remove favorite',
  'source.readLaterAction': 'Read later',
  'source.markDone': 'Done',
  'source.interests': 'Interests',
  'source.noInterests': 'No interest matches this source.',
  'source.interestClaims.one': '{count} claim',
  'source.interestClaims.many': '{count} claims',
  'source.interestPaused': 'paused',
  'source.interestMaybe': 'one mention',
  'source.rejectInterest': 'Not this interest',
  'source.acceptInterest': 'Yes, this fits',
  'source.queued': 'In your read-later queue',
  'source.queuedByInterests': 'Queued by an interest',

  'source.interestFromSource': 'Make an interest from this source',
};
