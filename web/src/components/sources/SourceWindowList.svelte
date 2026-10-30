<script lang="ts" generics="T">
  import { onMount, tick, type Snippet } from 'svelte';

  interface Props {
    /** The filtered total. The scroll height is count * rowHeight. */
    count: number;
    rowHeight: number;
    /** undefined while the page that holds the row is still in flight. */
    itemAt: (index: number) => T | undefined;
    /** ask the store for the pages covering [start, end). */
    ensure: (start: number, end: number) => void;
    /** changes when the loaded pages were discarded, so the visible range is read again. */
    epoch: number;
    row: Snippet<[T | undefined, number]>;
    threshold?: number;
    overscan?: number;
  }

  let {
    count,
    rowHeight,
    itemAt,
    ensure,
    epoch,
    row,
    threshold = 30,
    overscan = 6,
  }: Props = $props();

  let wrapper: HTMLDivElement | undefined = $state();
  let scroller: HTMLElement | null = null;
  let frame = 0;
  let start = $state(0);
  let end = $state(0);

  const totalHeight = $derived(count * rowHeight);
  const windowed = $derived(count > threshold);

  function clamp(value: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, value));
  }

  function findScroller(): HTMLElement | null {
    let element = wrapper?.parentElement ?? null;
    while (element) {
      const style = getComputedStyle(element);
      if (
        (style.overflowY === 'auto' || style.overflowY === 'scroll') &&
        element.scrollHeight > element.clientHeight + 1
      ) {
        return element;
      }
      element = element.parentElement;
    }
    return document.querySelector<HTMLElement>('.content-scroll');
  }

  function schedule(): void {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      recompute();
    });
  }

  function recompute(): void {
    if (!wrapper) return;
    if (count <= 0) {
      start = 0;
      end = 0;
      return;
    }
    if (!windowed) {
      start = 0;
      end = count;
      ensure(start, end);
      return;
    }
    const scrollElement = scroller ?? document.querySelector<HTMLElement>('.content-scroll');
    if (!scrollElement) {
      start = 0;
      end = Math.min(count, overscan * 2);
      ensure(start, end);
      return;
    }
    const scrollerRect = scrollElement.getBoundingClientRect();
    const wrapperRect = wrapper.getBoundingClientRect();
    const listTop = wrapperRect.top - scrollerRect.top + scrollElement.scrollTop;
    const viewTop = scrollElement.scrollTop - listTop;
    const viewBottom = viewTop + scrollElement.clientHeight;

    const first = Math.floor(viewTop / rowHeight) - overscan;
    const last = Math.ceil(viewBottom / rowHeight) + overscan;
    start = clamp(first, 0, Math.max(0, count - 1));
    end = clamp(last, start, count);
    // A list below the fold still renders a first page, so it never appears as an empty box.
    if (end <= start) end = Math.min(count, start + overscan * 2);
    ensure(start, end);
  }

  function bindScroller(): void {
    const next = findScroller();
    if (next !== scroller) {
      scroller?.removeEventListener('scroll', schedule);
      scroller = next;
      scroller?.addEventListener('scroll', schedule, { passive: true });
    }
    schedule();
  }

  onMount(() => {
    const observer = new ResizeObserver(() => {
      bindScroller();
      schedule();
    });
    if (wrapper) observer.observe(wrapper);
    if (wrapper?.parentElement) observer.observe(wrapper.parentElement);
    const page = document.querySelector<HTMLElement>('.content-scroll');
    if (page) observer.observe(page);
    const onResize = (): void => {
      bindScroller();
      schedule();
    };
    window.addEventListener('resize', onResize);
    bindScroller();

    return () => {
      window.removeEventListener('resize', onResize);
      observer.disconnect();
      scroller?.removeEventListener('scroll', schedule);
      scroller = null;
      if (frame) cancelAnimationFrame(frame);
    };
  });

  $effect(() => {
    // count changed (filter, first page), or the pages were discarded and must be read again.
    void count;
    void rowHeight;
    void epoch;
    void windowed;
    void tick().then(() => {
      bindScroller();
      schedule();
    });
  });
</script>

{#if count > 0}
  <div
    class="virtual-list {windowed ? 'virtual-list--windowed' : ''}"
    style:height={windowed ? `${totalHeight}px` : undefined}
    bind:this={wrapper}
  >
    {#each Array(Math.max(0, end - start)) as _, offset (start + offset)}
      {@const index = start + offset}
      {#if windowed}
        <div class="virtual-item" style:top={`${index * rowHeight}px`} style:height={`${rowHeight}px`}>
          {@render row(itemAt(index), index)}
        </div>
      {:else}
        {@render row(itemAt(index), index)}
      {/if}
    {/each}
  </div>
{/if}
