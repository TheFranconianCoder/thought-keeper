<script lang="ts">
  interface Props {
    name: string;
    size?: number;
    class?: string;
  }
  const { name, size = 16, class: cls = '' }: Props = $props();

  const paths: Record<string, string> = {
    // lenses
    claims: 'M4 4.5h6.5M4 9h6.5M4 13.5h4M14 4.5h2M14 9h2M14 13.5h2',
    wiki: 'M4 3.5h8.5a2 2 0 0 1 2 2v11H6a2 2 0 0 1-2-2z M4 14.5a2 2 0 0 1 2-2h8.5',
    sources: 'M3.5 5.2c0-1 2.7-1.8 6.5-1.8s6.5.8 6.5 1.8-2.7 1.8-6.5 1.8S3.5 6.2 3.5 5.2z M3.5 5.2v9.6c0 1 2.7 1.8 6.5 1.8s6.5-.8 6.5-1.8V5.2 M3.5 10c0 1 2.7 1.8 6.5 1.8s6.5-.8 6.5-1.8',
    attention: 'M10 2.8 17 16H3z M10 8v3.6 M10 14h.01',
    bookmarks: 'M5.5 3.5h9v13L10 13l-4.5 3.5z',
    ask: 'M10 2.5 11.8 7.2 16.5 9 11.8 10.8 10 15.5 8.2 10.8 3.5 9 8.2 7.2z M15.5 2.5l.7 1.8 1.8.7-1.8.7-.7 1.8-.7-1.8-1.8-.7 1.8-.7z',
    status: 'M3 10h3l1.8-4.5L11 14l1.8-4h4.2',

    // actions
    search: 'M8.8 3.2a5.6 5.6 0 1 0 3.9 9.6l3.5 3.5 M8.8 3.2a5.6 5.6 0 0 1 3.9 9.6',
    plus: 'M10 4v12 M4 10h12',
    close: 'M5 5l10 10 M15 5 5 15',
    check: 'M4 10.5 8 14.5 16 5.5',
    copy: 'M7 6.5h9v10H7z M5.5 13.5H4v-10h9v1.5',
    trash: 'M4 5.5h12 M7.5 5.5v-2h5v2 M6 5.5l.7 11h6.6l.7-11',
    refresh: 'M16.5 10a6.5 6.5 0 1 1-1.9-4.6 M16.5 3v4h-4',
    reset: 'M9.5 5.5 5 10l4.5 4.5 M16 5.5 11.5 10l4.5 4.5',
    tree: 'M4 4h4.5 M4 4v12h4.5 M4 10h4.5 M11 10h5 M11 4h5 M11 16h5',
    capture: 'M2 10s3-5.2 8-5.2S18 10 18 10s-3 5.2-8 5.2S2 10 2 10z M10 7.8a2.2 2.2 0 1 0 0 4.4 2.2 2.2 0 0 0 0-4.4',
    distill: 'M2.5 10h4 M10.5 4.5H18 M10.5 10H18 M10.5 15.5H18 M6.5 10 10.5 4.5 M6.5 10l4 5.5',
    menu: 'M3 5.5h14 M3 10h14 M3 14.5h14',
    chevronLeft: 'M12 4.5 6.5 10l5.5 5.5',
    chevronRight: 'M8 4.5 13.5 10 8 15.5',
    chevronDown: 'M5 8l5 5 5-5',
    arrowLeft: 'M16 10H4 M9 5l-5 5 5 5',
    arrowUpRight: 'M5.5 14.5 14.5 5.5 M7 5.5h7.5V13',
    external: 'M11 3.5h5.5V9 M16.5 3.5 9.5 10.5 M8 5H4.5v10.5H15V12',
    filter: 'M3 5h14 M6 10h8 M8.5 15h3',
    more: 'M5 10h.01 M10 10h.01 M15 10h.01',
    info: 'M10 2.8a7.2 7.2 0 1 0 0 14.4 7.2 7.2 0 0 0 0-14.4z M10 9v4 M10 6.5h.01',
    warning: 'M10 3 17.5 16H2.5z M10 8v4 M10 14.5h.01',
    error: 'M10 2.8a7.2 7.2 0 1 0 0 14.4 7.2 7.2 0 0 0 0-14.4z M7 7l6 6 M13 7l-6 6',
    clock: 'M10 2.8a7.2 7.2 0 1 0 0 14.4 7.2 7.2 0 0 0 0-14.4z M10 5.5V10l3 2',
    settings: 'M10 7.2a2.8 2.8 0 1 0 0 5.6 2.8 2.8 0 0 0 0-5.6z M10 2.5v2 M10 15.5v2 M2.5 10h2 M15.5 10h2 M4.7 4.7l1.4 1.4 M13.9 13.9l1.4 1.4 M15.3 4.7l-1.4 1.4 M6.1 13.9l-1.4 1.4',
    panelLeft: 'M3.5 4h13v12h-13z M8 4v12',
    spark: 'M10 2.5 11.8 7.2 16.5 9 11.8 10.8 10 15.5 8.2 10.8 3.5 9 8.2 7.2z',
    sun: 'M10 6.8a3.2 3.2 0 1 0 0 6.4 3.2 3.2 0 0 0 0-6.4z M10 2v2 M10 16v2 M2 10h2 M16 10h2 M4.3 4.3l1.4 1.4 M14.3 14.3l1.4 1.4 M15.7 4.3l-1.4 1.4 M5.7 14.3l-1.4 1.4',
    moon: 'M16.5 12.3A6.7 6.7 0 0 1 7.7 3.5a6.7 6.7 0 1 0 8.8 8.8z',
    auto: 'M10 2.8a7.2 7.2 0 1 0 0 14.4 7.2 7.2 0 0 0 0-14.4z',
    file: 'M5 2.8h6l4 4v10.4H5z M11 2.8v4h4',
    fileText: 'M5 2.8h6l4 4v10.4H5z M11 2.8v4h4 M7.5 10h5 M7.5 12.5h5 M7.5 15h3',
    filePdf: 'M5 2.8h6l4 4v10.4H5z M11 2.8v4h4 M7.5 10h5 M7.5 12.5h3',
    link: 'M8.2 11.8 11.8 8.2 M7.5 6l1.8-1.8a2.8 2.8 0 0 1 4 4L11.5 10 M12.5 14l-1.8 1.8a2.8 2.8 0 0 1-4-4L8.5 10',
    book: 'M4 3.5h4.5A2.5 2.5 0 0 1 11 6v11a2.5 2.5 0 0 0-2.5-2.5H4z M16 3.5h-4.5A2.5 2.5 0 0 0 9 6v11a2.5 2.5 0 0 1 2.5-2.5H16z',
    inbox: 'M3 11.5 5.5 4h9l2.5 7.5V16H3z M3 11.5h4l1 2h4l1-2h4',
    upload: 'M10 13.5V3.5 M6 7.5 10 3.5l4 4 M4 16.5h12',
    download: 'M10 3.5v10 M6 9.5l4 4 4-4 M4 16.5h12',
    edit: 'M4 16.5h3.5L16 8a2.1 2.1 0 0 0-3-3L4.5 13.5z M11.5 6.5l3 3',
    eye: 'M2 10s3-5.2 8-5.2S18 10 18 10s-3 5.2-8 5.2S2 10 2 10z M10 7.8a2.2 2.2 0 1 0 0 4.4 2.2 2.2 0 0 0 0-4.4',
    log: 'M4 4.5h12 M4 8h12 M4 11.5h8 M4 15h10',
    layers: 'M10 3 3.5 6.5 10 10l6.5-3.5z M3.5 10 10 13.5 16.5 10 M3.5 13.5 10 17l6.5-3.5',
    dot: 'M10 10h.01',
  };

  const filled: Record<string, string> = {
    auto: 'M10 2.8a7.2 7.2 0 0 0 0 14.4z',
  };
</script>

<svg
  class="ico {cls}"
  width={size}
  height={size}
  viewBox="0 0 20 20"
  fill="none"
  stroke="currentColor"
  stroke-width="1.5"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  <path d={paths[name] ?? paths.dot} />
  {#if filled[name]}<path d={filled[name]} fill="currentColor" stroke="none" />{/if}
</svg>

<style>
  .ico {
    flex: none;
    vertical-align: -0.14em;
  }
</style>
