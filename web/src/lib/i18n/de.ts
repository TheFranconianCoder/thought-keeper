import { commonDe } from './messages/common.de';
import { shellDe } from './messages/shell.de';
import { homeDe } from './messages/home.de';
import { askDe } from './messages/ask.de';
import { sourcesDe } from './messages/sources.de';
import { sourceDe } from './messages/source.de';
import { claimsDe } from './messages/claims.de';
import { wikiDe } from './messages/wiki.de';
import { statusDe } from './messages/status.de';
import { attentionDe } from './messages/attention.de';
import { assistantDe } from './messages/assistant.de';
import { interestsDe } from './messages/interests.de';

/** The German catalog. A missing key falls back to English, never to the key itself. */
export const de: Record<string, string> = {
  ...commonDe,
  ...shellDe,
  ...homeDe,
  ...askDe,
  ...sourcesDe,
  ...sourceDe,
  ...claimsDe,
  ...wikiDe,
  ...statusDe,
  ...attentionDe,
  ...assistantDe,
  ...interestsDe,
};
