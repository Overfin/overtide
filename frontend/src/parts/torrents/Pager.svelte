<!-- A list's pager: Previous, where it stands, Next; on top, the pages
     around this one and the page sizes too. url makes a page's address. -->
<script lang="ts">
  import type { Paging } from '../../lib/types-torrents';

  let { paging, url, full = false }: { paging: Paging; url: (page: number, size: number) => string; full?: boolean } = $props();

  const around = $derived.by(() => {
    const out: number[] = [];
    for (let p = Math.max(1, paging.page - 2); p <= Math.min(paging.totalPages, paging.page + 2); p++) out.push(p);
    return out;
  });
</script>

<div class="pager">
  {#if paging.page > 1}<a href={url(paging.page - 1, paging.pageSize)}>← Previous</a>{:else}<span class="disabled">← Previous</span>{/if}
  <span>Page {paging.page} of {paging.totalPages}</span>
  {#if full}
    {#each around as p (p)}{#if p === paging.page}<span class="current">{p}</span>{:else}<a href={url(p, paging.pageSize)}>{p}</a>{/if}{/each}
  {/if}
  {#if paging.page < paging.totalPages}<a href={url(paging.page + 1, paging.pageSize)}>Next →</a>{:else}<span class="disabled">Next →</span>{/if}
  {#if full}
    <div class="pagesize">
      Per page: {#each [25, 50, 100, 250] as n (n)}<a class:active={n === paging.pageSize} href={url(1, n)}>{n}</a>{/each}
    </div>
  {/if}
</div>
