/** Formatting helpers shared by every view. No domain logic lives here. */

export function fmtBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GiB`;
}

export function relTime(iso?: string, now = Date.now()): string {
  if (!iso) return '';
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return iso;
  const seconds = Math.max(0, Math.round((now - then) / 1000));
  if (seconds < 45) return 'just now';
  if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`;
  if (seconds < 86_400) return `${Math.round(seconds / 3600)}h ago`;
  if (seconds < 2_592_000) return `${Math.round(seconds / 86_400)}d ago`;
  return new Date(then).toLocaleDateString(undefined, { day: '2-digit', month: 'short', year: 'numeric' });
}

export function elapsed(iso: string | undefined, now = Date.now()): string {
  if (!iso) return '';
  const started = Date.parse(iso);
  if (Number.isNaN(started)) return '';
  const seconds = Math.max(0, Math.round((now - started) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

export function shortDate(iso?: string): string {
  if (!iso) return '';
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return iso;
  return new Date(parsed).toLocaleDateString(undefined, { day: '2-digit', month: 'short' });
}

export function clockTime(iso?: string): string {
  if (!iso) return '';
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return iso;
  return new Date(parsed).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

export function plural(count: number, singular: string, pluralForm = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : pluralForm}`;
}

export function hostOf(url?: string): string {
  if (!url) return '';
  try {
    return new URL(url).host.replace(/^www\./, '');
  } catch {
    return '';
  }
}

/** splitFrontmatter exposes a document's YAML header instead of hiding it.
 *
 *  The header is what a source declares about itself (a URL's address and fetch time, a page's
 *  provenance). A reader that strips it hides exactly the provenance the app exists to preserve.
 */
export function splitFrontmatter(text: string): { front: string; body: string } {
  if (!text.startsWith('---')) return { front: '', body: text };
  const end = text.indexOf('\n---', 3);
  if (end < 0) return { front: '', body: text };
  const front = text
    .slice(3, end)
    .split('\n')
    .map((line) => line.replace(/\s+$/, ''))
    .join('\n')
    .replace(/^\n+/, '')
    .replace(/\n+$/, '');
  return { front, body: text.slice(end + 4).replace(/^\s*\n/, '') };
}

const PRETTY_CAP = 512 * 1024;

/** prettyJson formats JSON that is meant to be read, but never blocks the UI on a huge file. */
export function prettyJson(text: string): string {
  if (text.length > PRETTY_CAP) return text;
  const trimmed = text.trim();
  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return text;
  try {
    return `${JSON.stringify(JSON.parse(trimmed), null, 2)}\n`;
  } catch {
    return text;
  }
}

export function isURL(value: string): boolean {
  return /^https?:\/\//i.test(value.trim());
}

/** firstLine keeps a server error readable in a one-line notice. */
export function firstLine(text: string): string {
  return (text.split('\n').find((line) => line.trim()) ?? '').trim();
}

export function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}
