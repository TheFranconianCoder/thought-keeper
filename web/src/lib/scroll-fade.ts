/** scrollFade masks the edges of a scroll container while it can still scroll in that direction.
 *
 *  It is used for the small, independently scrolling surfaces in the app — topic clouds, tag clouds,
 *  list panes and the palette results — so a cut-off row/chip is visibly cut off instead of looking
 *  like an abrupt end. The mask is removed entirely when the content fits.
 */
export interface ScrollFadeOptions {
  size?: number;
  axis?: 'y' | 'x';
}

export function scrollFade(node: HTMLElement, options: ScrollFadeOptions = {}) {
  let size = options.size ?? 18;
  let axis = options.axis ?? 'y';
  let frame = 0;

  const update = (): void => {
    const vertical = axis === 'y';
    const position = vertical ? node.scrollTop : node.scrollLeft;
    const client = vertical ? node.clientHeight : node.clientWidth;
    const total = vertical ? node.scrollHeight : node.scrollWidth;
    const overflow = total > client + 1;

    if (!overflow) {
      node.style.removeProperty('mask-image');
      node.style.removeProperty('-webkit-mask-image');
      return;
    }

    const start = position <= 1 ? 0 : size;
    const end = position + client >= total - 1 ? 0 : size;
    const gradient = vertical
      ? `linear-gradient(to bottom, transparent 0, #000 ${start}px, #000 calc(100% - ${end}px), transparent 100%)`
      : `linear-gradient(to right, transparent 0, #000 ${start}px, #000 calc(100% - ${end}px), transparent 100%)`;
    node.style.setProperty('mask-image', gradient);
    node.style.setProperty('-webkit-mask-image', gradient);
  };

  const schedule = (): void => {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      update();
    });
  };

  const observer = new ResizeObserver(schedule);
  observer.observe(node);
  // A list can grow without changing the container's box, so observe its children too.
  const mutations = new MutationObserver(schedule);
  mutations.observe(node, { childList: true, subtree: true });
  node.addEventListener('scroll', schedule, { passive: true });
  schedule();

  return {
    update(next: ScrollFadeOptions = {}) {
      size = next.size ?? size;
      axis = next.axis ?? axis;
      schedule();
    },
    destroy() {
      if (frame) cancelAnimationFrame(frame);
      observer.disconnect();
      mutations.disconnect();
      node.removeEventListener('scroll', schedule);
      node.style.removeProperty('mask-image');
      node.style.removeProperty('-webkit-mask-image');
    },
  };
}
