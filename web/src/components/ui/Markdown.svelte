<script lang="ts">
  import { renderMarkdown } from '../../lib/markdown';

  const {
    text = '',
    topics = [],
    footnotes = false,
    claimTopic = '',
    wikilinks = true,
    class: cls = '',
  }: {
    text?: string;
    topics?: string[];
    footnotes?: boolean;
    claimTopic?: string;
    wikilinks?: boolean;
    class?: string;
  } = $props();

  const html = $derived(renderMarkdown(text, { topics, footnotes, claimTopic, wikilinks }));
  let root: HTMLDivElement | undefined = $state();

  $effect(() => {
    const element = root;
    if (!element) return;
    element.addEventListener('click', handleClick);
    return () => element.removeEventListener('click', handleClick);
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
