<script lang="ts">
  /** Frontmatter renders the YAML header as it was written, but turns bare http(s) URLs into links.
   *  A URL source's header carries the address it came from — the one place a reader can click
   *  through to the page — and a plain <pre> made it dead text. */
  interface Props {
    text: string;
  }

  const { text }: Props = $props();

  const segments = $derived.by((): Array<{ text: string; url?: string }> => {
    const out: Array<{ text: string; url?: string }> = [];
    let last = 0;
    for (const match of text.matchAll(/https?:\/\/[^\s]+/g)) {
      const at = match.index ?? 0;
      if (at > last) out.push({ text: text.slice(last, at) });
      out.push({ text: match[0], url: match[0] });
      last = at + match[0].length;
    }
    if (last < text.length) out.push({ text: text.slice(last) });
    return out;
  });
</script>

<div class="frontmatter">{#each segments as segment, index (index)}{#if segment.url}<a href={segment.url} target="_blank" rel="noopener noreferrer" title={segment.url}>{segment.text}</a>{:else}{segment.text}{/if}{/each}</div>

<style>
  .frontmatter {
    margin: 0 0 16px;
    padding: 10px 12px;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg-muted);
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.55;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .frontmatter a {
    color: var(--accent);
    text-decoration: underline;
    text-underline-offset: 2px;
  }
</style>
