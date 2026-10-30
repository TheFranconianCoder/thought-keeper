import { api } from '../api';
import type { Run } from '../api/types';

/** waitForRun polls one run until it stops running. It is the deterministic fallback for actions whose
 *  outcome the UI must report even if an SSE frame is missed (lint, ask). The stream remains the
 *  primary path; this is a safety net, not a polling architecture. */
export async function waitForRun(runID: string, timeoutMs = 5 * 60_000): Promise<Run | null> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      const run = await api.run(runID);
      if (run.status !== 'running') return run;
    } catch {
      /* retry until the deadline */
    }
    if (Date.now() >= deadline) return null;
    await new Promise((resolve) => setTimeout(resolve, 900));
  }
}
