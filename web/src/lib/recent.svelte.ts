/**
 * The things the reader opened last: sources, claims, wiki pages and saved questions. It is a
 * LOCAL memory (localStorage), not a vault record: "continue where you left off" is a convenience,
 * not part of the knowledge base. A detail is recorded once; revisiting it moves it to the front.
 */
export type RecentKind = 'source' | 'claim' | 'wiki' | 'ask';

export interface RecentEntry {
  kind: RecentKind;
  id: string;
  title: string;
  at: number;
}

const STORAGE_KEY = 'tk-recent';
const MAX = 8;
const KINDS: readonly RecentKind[] = ['source', 'claim', 'wiki', 'ask'];

function load(): RecentEntry[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((entry): entry is RecentEntry => {
        if (!entry || typeof entry !== 'object') return false;
        const value = entry as Record<string, unknown>;
        return (
          typeof value.id === 'string' &&
          typeof value.title === 'string' &&
          typeof value.at === 'number' &&
          KINDS.includes(value.kind as RecentKind)
        );
      })
      .slice(0, MAX);
  } catch {
    return [];
  }
}

class RecentStore {
  items = $state<RecentEntry[]>(load());

  visit(kind: RecentKind, id: string, title: string): void {
    const value = title.trim();
    if (!id || !value) return;
    const front = this.items[0];
    if (front && front.kind === kind && front.id === id) return;
    const rest = this.items.filter((entry) => !(entry.kind === kind && entry.id === id));
    this.items = [{ kind, id, title: value, at: Date.now() }, ...rest].slice(0, MAX);
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(this.items));
    } catch {
      /* storage can be unavailable */
    }
  }

  clear(): void {
    this.items = [];
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      /* storage can be unavailable */
    }
  }
}

export const recent = new RecentStore();
