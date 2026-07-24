<script lang="ts">
  export let name: 'sparkles' | 'search' | 'image' | 'image-plus' | 'upload';
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
