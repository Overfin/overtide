<!-- A device's bar, drawn to its capacity: each service's share (Storage
     page) or Overtide's used bytes (Home), unmanaged files, free space, and
     what other software uses at the right; the cleanup target as a line.
     SVG, since the page's rules forbid inline styles. -->
<script lang="ts">
  import { human, share, type StorageDevice } from '../../lib/types-storage';

  let { device, detail = false, target = null }: { device: StorageDevice; detail?: boolean; target?: number | null } = $props();

  interface Segment {
    kind: string;
    bytes: number;
    label: string;
  }

  const cap = $derived(device.capacityBytes);
  const segments = $derived.by((): (Segment & { x: number; width: number })[] => {
    const left: Segment[] = detail
      ? [
          ...device.claimed.map((c, i) => ({ kind: `claim c${i % 4}`, bytes: c.bytes, label: `${c.service}: ${human(c.bytes)}` })),
          { kind: 'unmanaged', bytes: device.unmanagedBytes, label: `Unmanaged: ${human(device.unmanagedBytes)}` },
          { kind: 'free', bytes: device.freeBytes, label: `Free: ${human(device.freeBytes)}` },
        ]
      : [
          { kind: 'used', bytes: device.overtideUsedBytes, label: '' },
          { kind: 'free', bytes: device.freeBytes, label: '' },
        ];
    let x = 0;
    const out = left.map((s) => {
      const width = Number(share(s.bytes, cap));
      const placed = { ...s, x, width };
      x += width;
      return placed;
    });
    // What other software uses: last, at the right.
    const elsewhere = Number(share(device.elsewhereBytes, cap));
    out.push({ kind: 'elsewhere', bytes: device.elsewhereBytes, label: detail ? `Used elsewhere: ${human(device.elsewhereBytes)}` : '', x: 100 - elsewhere, width: elsewhere });
    return out;
  });
  // The target is a share of what Overtide manages (usable), drawn on a
  // bar of the disk's capacity.
  const targetX = $derived(target === null || !cap ? null : (target * device.usableBytes) / cap);
</script>

<svg class="storage-bar" viewBox="0 0 100 30" preserveAspectRatio="none" role="img" aria-label="Disk usage">
  {#each segments as s, i (i)}
    <rect class="segment {s.kind}" x={s.x} y="0" width={s.width} height="30">{#if s.label}<title>{s.label}</title>{/if}</rect>
  {/each}
  {#if targetX !== null}
    <line class="threshold target" x1={targetX} x2={targetX} y1="0" y2="30" vector-effect="non-scaling-stroke"><title>Target: {target!.toFixed(1)}%</title></line>
  {/if}
</svg>
