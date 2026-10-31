import { api } from '../api';
import type { Interest } from '../api/types';
import { readError } from '../read';

/**
 * The reader's standing rules. The list is small (a handful), so it is read whole and kept — the
 * Sources rows and the Start card need the NAME of the interest that queued a source, not just its
 * slug, and the manager needs the same list.
 *
 * The rules are the truth in the vault (interests/<slug>.md); this store is a read of them.
 */
class InterestsStore {
  items = $state<Interest[]>([]);
  loaded = $state(false);
  loading = $state(false);
  error = $state('');

  private seq = 0;
  private run: Promise<void> | null = null;

  /** load reads the rules once. `force` re-reads even when a list is already there. */
  load(force = false): Promise<void> {
    if (this.run) return this.run;
    if (this.loaded && !force) return Promise.resolve();
    const seq = ++this.seq;
    this.loading = true;
    this.run = (async () => {
      try {
        const result = await api.interests();
        if (seq !== this.seq) return;
        this.items = result.interests ?? [];
        this.loaded = true;
        this.error = '';
      } catch (error) {
        if (seq !== this.seq) return;
        this.loaded = true;
        this.error = readError(error);
      } finally {
        if (seq === this.seq) {
          this.loading = false;
          this.run = null;
        }
      }
    })();
    return this.run;
  }

  /** refresh re-reads after a write, including one made in another window. The previous list stays
   *  on screen: a manager that flashes skeletons on every save is a manager nobody trusts. */
  async refresh(): Promise<void> {
    await this.load(true);
  }

  /** nameOf is what a queue row or the source rail shows; an unknown slug (a deleted rule) falls
   *  back to itself, which is honest rather than pretending the rule still exists. */
  nameOf(slug: string): string {
    return this.items.find((item) => item.slug === slug)?.name ?? slug;
  }

  bySlug(slug: string): Interest | undefined {
    return this.items.find((item) => item.slug === slug);
  }
}

export const interests = new InterestsStore();
