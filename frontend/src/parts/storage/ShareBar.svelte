<!-- A disk's space up to its target, shared between its root folders: one
     part per folder, sized by its share, filled with what it holds (past
     its share, in warning colour), and a cursor between each two parts,
     moved with the mouse or the arrow keys. The shares always add up to
     100. SVG, since the page's rules forbid inline styles. -->
<script lang="ts">
  import { human } from '../../lib/types-storage';

  let {
    folders,
    shares = $bindable(),
    capacity,
  }: { folders: { path: string; bytes: number }[]; shares: number[]; capacity: number } = $props();

  let svg = $state<SVGSVGElement | null>(null);
  let dragging = $state(-1);

  // Where each part starts, 0 to 100.
  const starts = $derived(shares.map((_, i) => shares.slice(0, i).reduce((a, b) => a + b, 0)));
  // What each folder holds, as a part of the whole bar.
  const held = $derived(folders.map((f) => (capacity > 0 ? (f.bytes / capacity) * 100 : 0)));

  // move puts cursor i (between parts i and i+1) at x, 0 to 100.
  function move(i: number, x: number) {
    const low = starts[i];
    const high = starts[i] + shares[i] + shares[i + 1];
    const at = Math.round(Math.min(Math.max(x, low), high));
    const next = [...shares];
    next[i] = at - low;
    next[i + 1] = high - at;
    shares = next;
  }

  function pointerX(event: PointerEvent): number {
    const box = svg!.getBoundingClientRect();
    return ((event.clientX - box.left) / box.width) * 100;
  }

  function down(i: number, event: PointerEvent) {
    dragging = i;
    (event.currentTarget as Element).setPointerCapture(event.pointerId);
  }

  function key(i: number, event: KeyboardEvent) {
    const step = event.shiftKey ? 5 : 1;
    const x = starts[i] + shares[i];
    if (event.key === 'ArrowLeft' || event.key === 'ArrowDown') move(i, x - step);
    else if (event.key === 'ArrowRight' || event.key === 'ArrowUp') move(i, x + step);
    else return;
    event.preventDefault();
  }

  const name = (path: string) => path.split('/').filter(Boolean).pop() ?? path;
</script>

<svg
  class="storage-bar share-bar"
  bind:this={svg}
  viewBox="0 0 100 30"
  preserveAspectRatio="none"
  role="group"
  aria-label="Root folders' shares"
  onpointermove={(e) => dragging >= 0 && move(dragging, pointerX(e))}
  onpointerup={() => (dragging = -1)}
>
  {#each folders as folder, i (folder.path)}
    <rect class="share c{i % 4}" x={starts[i]} y="0" width={shares[i]} height="30"><title>{folder.path}: {shares[i]}%</title></rect>
    <!-- What it holds; filled in warning colour when past its share. -->
    <rect class="segment {held[i] > shares[i] ? 'over' : `c${i % 4}`}" x={starts[i]} y="8" width={Math.min(held[i], shares[i])} height="14"><title>{folder.path}: {human(folder.bytes)}</title></rect>
  {/each}
  {#each shares.slice(0, -1) as _, i (i)}
    <g
      class="cursor"
      role="slider"
      tabindex="0"
      aria-label="Between {name(folders[i].path)} and {name(folders[i + 1].path)}"
      aria-valuemin={starts[i]}
      aria-valuemax={starts[i] + shares[i] + shares[i + 1]}
      aria-valuenow={starts[i] + shares[i]}
      aria-valuetext="{name(folders[i].path)} {shares[i]}%, {name(folders[i + 1].path)} {shares[i + 1]}%"
      onpointerdown={(e) => down(i, e)}
      onkeydown={(e) => key(i, e)}
    >
      <rect class="grip" x={starts[i] + shares[i] - 2} y="0" width="4" height="30" />
      <line x1={starts[i] + shares[i]} x2={starts[i] + shares[i]} y1="0" y2="30" vector-effect="non-scaling-stroke" />
    </g>
  {/each}
</svg>
<ul class="storage-legend">
  {#each folders as folder, i (folder.path)}
    <li class="c{i % 4}">
      <span class="swatch"></span>
      {folder.path}: {shares[i]}% ({human((capacity * shares[i]) / 100)}), holds {human(folder.bytes)}
      {#if held[i] > shares[i]}<span class="warn">· over its share</span>{/if}
    </li>
  {/each}
</ul>
