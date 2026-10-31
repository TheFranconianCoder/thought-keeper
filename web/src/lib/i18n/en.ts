import { commonEn } from './messages/common.en';
import { shellEn } from './messages/shell.en';
import { homeEn } from './messages/home.en';
import { askEn } from './messages/ask.en';
import { sourcesEn } from './messages/sources.en';
import { sourceEn } from './messages/source.en';
import { claimsEn } from './messages/claims.en';
import { wikiEn } from './messages/wiki.en';
import { statusEn } from './messages/status.en';
import { attentionEn } from './messages/attention.en';
import { assistantEn } from './messages/assistant.en';
import { interestsEn } from './messages/interests.en';

/** The English catalog: every key the interface asks for, and the fallback for every other locale. */
export const en: Record<string, string> = {
  ...commonEn,
  ...shellEn,
  ...homeEn,
  ...askEn,
  ...sourcesEn,
  ...sourceEn,
  ...claimsEn,
  ...wikiEn,
  ...statusEn,
  ...attentionEn,
  ...assistantEn,
  ...interestsEn,
};
