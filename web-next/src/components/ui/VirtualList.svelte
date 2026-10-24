<script lang="ts" generics="T">
  import { onMount, tick, type Snippet } from 'svelte';

  interface Props {
    items: T[];
    /** Fixed height of one row, in px. Rows are only windowed above `threshold`. */
    rowHeight: number;
    key: (item: T, index: number) => string;
    row: Snippet<[T, number]>;
    /** Below this many items the list is rendered normally; it is not worth a window. */
    threshold?: number;
    overscan?: number;
    class?: string;
  }

  let {
    items,
    rowHeight,
    key,
    row,
    threshold = 30,
    overscan = 6,
    class: cls = '',
  }: Props = $props();

  let wrapper: HTMLDivElement | undefined = $state();
  let scroller: HTMLElement | null = null;
  let frame = 0;
  let start = $state(0);
  let end = $state(12);

  const windowed = $derived(items.length > threshold);
  const totalHeight = $derived(items.length * rowHeight);
  const visible = $derived(windowed ? items.slice(start, end) : items);

  function clamp(value: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, value));
  }

  /** The list may scroll with the page (Sources) or inside its own pane (a split list). Walk up
   *  until an element actually scrolls; the page scroller is the fallback for a short list. */
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
    if (!windowed) {
      start = 0;
      end = items.length;
      return;
    }
    const count = items.length;
    const scrollElement = scroller ?? document.querySelector<HTMLElement>('.content-scroll');
    if (!scrollElement) {
      start = 0;
      end = Math.min(count, overscan * 2);
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
    // Items changing (filter, SSE refresh) changes the total height and may change which ancestor
    // scrolls. Re-resolve after the DOM has applied the new count.
    void items.length;
    void rowHeight;
    void windowed;
    void tick().then(() => {
      bindScroller();
      schedule();
    });
  });
</script>

<div
  class="virtual-list {windowed ? 'virtual-list--windowed' : ''} {cls}"
  style:height={windowed ? `${totalHeight}px` : undefined}
  bind:this={wrapper}
>
  {#each visible as item, index (key(item, windowed ? start + index : index))}
    {#if windowed}
      <div
        class="virtual-item"
        style:top={`${(start + index) * rowHeight}px`}
        style:height={`${rowHeight}px`}
      >
        {@render row(item, start + index)}
      </div>
    {:else}
      {@render row(item, index)}
    {/if}
  {/each}
</div>
