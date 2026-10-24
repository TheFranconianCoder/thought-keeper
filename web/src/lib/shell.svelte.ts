/** documentTitle is the text for the tab (and, where the platform still has one, the window title):
 *  what you are looking at, then the app — and what the app is doing while it does something. */
export function documentTitle(where: string, running?: string | null): string {
	if (running) return `${running}… · Thought Keeper`;
	if (!where) return 'Thought Keeper';
	return `${where} · Thought Keeper`;
}
