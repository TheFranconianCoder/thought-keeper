<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { tick } from 'svelte';
  import { search } from '../../lib/stores/search.svelte';
  import { closeSearch } from '../../lib/overlays';
  import { claimPath, navigate, sourcePath } from '../../lib/router.svelte';
  import { searchOpen } from '../../lib/overlays';
  import { scrollFade } from '../../lib/scroll-fade';
  import Icon from '../ui/Icon.svelte';

  const open = $derived(searchOpen());
  let input: HTMLInputElement | undefined = $state();

  $effect(() => {
    if (!open) return;
    void tick().then(() => input?.focus());
    const onKey = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        event.preventDefault();
        close();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  function close(): void {
    closeSearch();
    search.reset();
  }

  function pick(index = search.active): void {
    const hit = search.hits[index];
    if (!hit) return;
    const target =
      search.layer === 'sources' || hit.kind === 'source' ? sourcePath(hit.id) : claimPath(hit.id);
    search.reset();
    navigate(target);
  }

  function onKey(event: KeyboardEvent): void {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      search.move(1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      search.move(-1);
    } else if (event.key === 'Enter') {
      event.preventDefault();
      pick();
    }
  }
</script>

{#if open}
  <div class="palette-overlay">
    <button class="palette-bg" aria-label="Close search" onclick={close}></button>
    <div class="palette" role="dialog" aria-modal="true" aria-label="Search">
      <div class="palette-input">
        <Icon name="search" size={17} />
        <input
          use:clearable
          bind:this={input}
          value={search.query}
          placeholder={search.layer === 'atoms' ? 'Search claims…' : 'Search sources…'}
          autocomplete="off"
          spellcheck="false"
          aria-label="Search"
          oninput={(event) => search.setQuery(event.currentTarget.value)}
          onkeydown={onKey}
        />
        <div class="seg" role="tablist" aria-label="Search layer">
          <button
            type="button"
            class:on={search.layer === 'atoms'}
            onclick={() => {
              search.setLayer('atoms');
              input?.focus();
            }}>Claims</button
          >
          <button
            type="button"
            class:on={search.layer === 'sources'}
            onclick={() => {
              search.setLayer('sources');
              input?.focus();
            }}>Sources</button
          >
        </div>
      </div>

      <div class="palette-results" use:scrollFade>
        {#if search.busy}
          <p class="palette-note"><span class="spinner"></span> Searching…</p>
        {:else if search.error}
          <p class="palette-note palette-note--error">{search.error}</p>
        {:else if search.query.trim().length < 2}
          <p class="palette-note">Type at least two characters.</p>
        {:else if search.hits.length === 0}
          <p class="palette-note">
            No {search.layer === 'atoms' ? 'claim' : 'source'} matches “{search.query.trim()}”.
          </p>
        {:else}
          {#each search.hits as hit, index (hit.id)}
            <button
              class="hit"
              class:active={index === search.active}
              onclick={() => pick(index)}
              onmouseenter={() => (search.active = index)}
            >
              <Icon name={search.layer === 'atoms' ? 'claims' : 'sources'} size={15} />
              <span class="hit-main">
                <span class="hit-title">{hit.title}</span>
                <span class="hit-snippet">{hit.snippet}</span>
              </span>
              <span class="hit-kind">{search.layer === 'atoms' ? 'claim' : 'source'}</span>
            </button>
          {/each}
        {/if}
      </div>

      <div class="palette-hint">
        <span><span class="kbd">↑</span><span class="kbd">↓</span> navigate</span>
        <span><span class="kbd">↵</span> open</span>
        <span><span class="kbd">esc</span> close</span>
      </div>
    </div>
  </div>
{/if}

<style>
  .palette-overlay {
    position: fixed;
    inset: 0;
    z-index: 70;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: min(15vh, 130px) 16px 16px;
  }

  .palette-bg {
    position: absolute;
    inset: 0;
    border: 0;
    background: rgb(0 0 0 / 32%);
    backdrop-filter: blur(3px);
  }

  .palette {
    position: relative;
    z-index: 1;
    width: min(650px, 100%);
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
    animation: palette-in 0.16s var(--ease);
  }

  .palette-input {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 11px 12px;
    border-bottom: 1px solid var(--line);
    color: var(--faint);
  }

  .palette-input input {
    flex: 1;
    min-width: 0;
    height: 28px;
    border: 0;
    background: transparent;
    color: var(--ink);
    font-size: 15px;
    outline: none;
  }

  .palette-input input::placeholder {
    color: var(--faint);
  }

  .palette-results {
    max-height: min(52vh, 420px);
    overflow: auto;
    padding: 5px;
  }

  .palette-note {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    padding: 14px 12px;
    color: var(--muted);
    font-size: 12.5px;
  }

  .palette-note--error {
    color: var(--danger);
  }

  .hit {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 8px 9px;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--ink-2);
    text-align: left;
  }

  .hit:hover,
  .hit.active {
    background: var(--bg-hover);
  }

  .hit :global(.ico) {
    color: var(--muted);
  }

  .hit.active :global(.ico) {
    color: var(--accent);
  }

  .hit-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 1px;
  }

  .hit-title {
    overflow: hidden;
    color: var(--ink);
    font-size: 13px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .hit-snippet {
    overflow: hidden;
    color: var(--faint);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .hit-kind {
    color: var(--faint);
    font-size: 10.5px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .palette-hint {
    display: flex;
    gap: 16px;
    padding: 8px 12px;
    border-top: 1px solid var(--line);
    background: var(--bg-muted);
    color: var(--faint);
    font-size: 10.5px;
  }

  .palette-hint span {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }

  @keyframes palette-in {
    from {
      opacity: 0;
      transform: translateY(-5px) scale(0.995);
    }
  }
</style>
