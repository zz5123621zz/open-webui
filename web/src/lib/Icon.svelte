<script lang="ts">
  type IconName =
    | 'alert'
    | 'archive'
    | 'chat'
    | 'check'
    | 'chevron-down'
    | 'close'
    | 'copy'
    | 'edit'
    | 'eye'
    | 'globe'
    | 'image'
    | 'image-plus'
    | 'info'
    | 'lock'
    | 'logout'
    | 'menu'
    | 'moon'
    | 'more'
    | 'plan'
    | 'pin'
    | 'pin-off'
    | 'plus'
    | 'refresh'
    | 'restore'
    | 'retry'
    | 'search'
    | 'send'
    | 'shield'
    | 'sparkles'
    | 'stop'
    | 'sun'
    | 'theme'
    | 'trash'
    | 'upload';

  export let name: IconName;
  export let size = 16;
</script>

<svg
  class="app-icon"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="1.8"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
  focusable="false"
>
  {#if name === 'sparkles'}
    <path d="m12 3-1.45 3.55L7 8l3.55 1.45L12 13l1.45-3.55L17 8l-3.55-1.45Z" />
    <path d="m6 14-.85 2.15L3 17l2.15.85L6 20l.85-2.15L9 17l-2.15-.85Z" />
    <path d="m18 14-.65 1.35L16 16l1.35.65L18 18l.65-1.35L20 16l-1.35-.65Z" />
  {:else if name === 'alert'}
    <path d="M12 3 2.8 19h18.4Z" />
    <path d="M12 9v4" />
    <path d="M12 16.5h.01" />
  {:else if name === 'archive'}
    <path d="M4 7h16v13H4z" />
    <path d="M3 4h18v3H3zM9 11h6" />
  {:else if name === 'chat'}
    <path d="M5 5.5h14v10H9l-4 3v-13Z" />
    <path d="M9 10h6" />
  {:else if name === 'check'}
    <path d="m5 12 4 4L19 6" />
  {:else if name === 'chevron-down'}
    <path d="m6 9 6 6 6-6" />
  {:else if name === 'close'}
    <path d="m6 6 12 12M18 6 6 18" />
  {:else if name === 'copy'}
    <rect x="8" y="8" width="11" height="11" rx="2" />
    <path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" />
  {:else if name === 'edit'}
    <path d="m4 20 4.3-1 10-10a2.1 2.1 0 0 0-3-3l-10 10Z" />
    <path d="m14 7 3 3" />
  {:else if name === 'eye'}
    <path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z" />
    <circle cx="12" cy="12" r="2.5" />
  {:else if name === 'globe'}
    <circle cx="12" cy="12" r="9" />
    <path d="M3 12h18M12 3a15 15 0 0 1 0 18M12 3a15 15 0 0 0 0 18" />
  {:else if name === 'search'}
    <circle cx="11" cy="11" r="6" />
    <path d="m16 16 4 4" />
  {:else if name === 'image'}
    <rect x="3" y="4" width="18" height="16" rx="2.5" />
    <circle cx="9" cy="9" r="1.5" />
    <path d="m4 17 4.5-4.5 3.5 3 2.5-2.5 5.5 5" />
  {:else if name === 'image-plus'}
    <rect x="3" y="4" width="15" height="16" rx="2.5" />
    <circle cx="8" cy="9" r="1.5" />
    <path d="m4 17 4-4 3 3 2-2 4 4" />
    <path d="M20 7v6M17 10h6" />
  {:else if name === 'info'}
    <circle cx="12" cy="12" r="9" />
    <path d="M12 11v6M12 7h.01" />
  {:else if name === 'lock'}
    <rect x="5" y="10" width="14" height="10" rx="2" />
    <path d="M8 10V7a4 4 0 0 1 8 0v3" />
  {:else if name === 'logout'}
    <path d="M10 5H5v14h5M14 8l4 4-4 4M8 12h10" />
  {:else if name === 'menu'}
    <path d="M4 7h16M4 12h16M4 17h16" />
  {:else if name === 'moon'}
    <path d="M20 15.5A8 8 0 0 1 8.5 4 8.5 8.5 0 1 0 20 15.5Z" />
  {:else if name === 'more'}
    <circle cx="5" cy="12" r="1" fill="currentColor" stroke="none" />
    <circle cx="12" cy="12" r="1" fill="currentColor" stroke="none" />
    <circle cx="19" cy="12" r="1" fill="currentColor" stroke="none" />
  {:else if name === 'plan'}
    <path d="M6 4h12v16H6z" />
    <path d="M9 8h6M9 12h6M9 16h4" />
  {:else if name === 'pin'}
    <path d="m9 3 6 0-.8 5.1 3.3 3.3v1.1h-11v-1.1l3.3-3.3Z" />
    <path d="M12 12.5V21" />
  {:else if name === 'pin-off'}
    <path d="M3 3l18 18" />
    <path d="m9.8 3h5.2l-.8 5.1 3.3 3.3v1.1h-5M8.7 12.5H6.5v-1.1l2.1-2.1M12 15.8V21" />
  {:else if name === 'plus'}
    <path d="M12 5v14M5 12h14" />
  {:else if name === 'refresh' || name === 'retry'}
    <path d="M20 7v5h-5" />
    <path d="M19 12a7 7 0 1 1-2-5" />
  {:else if name === 'restore'}
    <path d="M4 7h16v13H4z" />
    <path d="M3 4h18v3H3zM12 17V11M9 14l3-3 3 3" />
  {:else if name === 'send'}
    <path d="m4 4 17 8-17 8 3-8Z" />
    <path d="M7 12h14" />
  {:else if name === 'shield'}
    <path d="M12 3 5 6v5c0 4.6 2.8 7.8 7 10 4.2-2.2 7-5.4 7-10V6Z" />
    <path d="m9 12 2 2 4-5" />
  {:else if name === 'stop'}
    <rect x="7" y="7" width="10" height="10" rx="2" fill="currentColor" stroke="none" />
  {:else if name === 'sun'}
    <circle cx="12" cy="12" r="3.5" />
    <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
  {:else if name === 'theme'}
    <circle cx="12" cy="12" r="9" />
    <path d="M12 3v18A9 9 0 0 0 12 3Z" fill="currentColor" stroke="none" />
  {:else if name === 'trash'}
    <path d="M4 7h16M9 7V4h6v3M7 7l1 13h8l1-13M10 11v5M14 11v5" />
  {:else}
    <path d="M12 16V4" />
    <path d="m7 9 5-5 5 5" />
    <path d="M5 15v4h14v-4" />
  {/if}
</svg>

<style>
  .app-icon {
    display: block;
    flex: 0 0 auto;
  }
</style>
