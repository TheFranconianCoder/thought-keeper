<script lang="ts">
  import { renderMarkdown } from '../../lib/markdown';
  import { resolveSourceLinks } from '../../lib/source-links';

  const {
    text = '',
    topics = [],
    footnotes = false,
    claimTopic = '',
    wikilinks = true,
    resolveSources = false,
    class: cls = '',
  }: {
    text?: string;
    topics?: string[];
    footnotes?: boolean;
    claimTopic?: string;
    wikilinks?: boolean;
    /** Open a link that already IS a source in the vault instead of on the web. */
    resolveSources?: boolean;
    class?: string;
  } = $props();

  const html = $derived(renderMarkdown(text, { topics, footnotes, claimTopic, wikilinks }));
  let root: HTMLDivElement | undefined = $state();
  let generation = 0;

  $effect(() => {
    const element = root;
    if (!element) return;
    element.addEventListener('click', handleClick);
    return () => element.removeEventListener('click', handleClick);
  });

  $effect(() => {
    if (!resolveSources) return;
    const element = root;
    if (!element) return;
    // The HTML is the dependency: new text means new anchors, and they are resolved again.
    void html;
    const current = ++generation;
    const anchors = [...element.querySelectorAll<HTMLAnchorElement>('a[href^="http"]')];
    const urls = [...new Set(anchors.map((link) => link.href))];
    if (urls.length === 0) return;
    void resolveSourceLinks(urls).then((links) => {
      if (current !== generation || !root) return;
      for (const link of root.querySelectorAll<HTMLAnchorElement>('a[href^="http"]')) {
        const hash = links.get(link.href);
        if (!hash) continue;
        // The vault has this page: the link opens the source, not the web. The address is internal
        // now, so the browser treats it like every other link into the app.
        link.setAttribute('href', `#/sources/${hash}`);
        link.removeAttribute('target');
        link.removeAttribute('rel');
      }
    });
  });

  function handleClick(event: MouseEvent): void {
    const target = event.target instanceof Element ? event.target : null;
    const link = target?.closest<HTMLAnchorElement>('a[href^="#cite-"], a[href^="#cite-ref-"]');
    if (!link) return;
    event.preventDefault();
    const id = (link.getAttribute('href') ?? '').slice(1);
    const entry = id ? document.getElementById(id) : null;
    if (!entry) return;
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    entry.scrollIntoView({ behavior: reduced ? 'auto' : 'smooth', block: 'center' });
    entry.classList.remove('cite-flash');
    void (entry as HTMLElement).offsetWidth;
    entry.classList.add('cite-flash');
    window.setTimeout(() => entry.classList.remove('cite-flash'), 1400);
  }
</script>

<div class="markdown {cls}" bind:this={root}>{@html html}</div>
