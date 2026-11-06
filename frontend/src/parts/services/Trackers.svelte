<!-- Prowlarr's trackers, with their limits and what they were asked: what
     paces adding's searches. Shown with a Prowlarr only. -->
<script lang="ts">
  import { Card, Badge, Notice } from '@overfin/ui';
  import { get } from '../../lib/api';

  interface Tracker {
    name: string;
    limit: number;
    per: 'hour' | 'day';
    used: number;
    searches: number;
  }

  let data = $state<{ prowlarr: boolean; trackers: Tracker[]; error?: string } | null>(null);

  get<{ prowlarr: boolean; trackers: Tracker[]; error?: string }>('/api/adding/trackers').then(
    (d) => (data = d),
    () => (data = null),
  );
</script>

{#if data?.prowlarr}
  <Card title="Trackers" description="From Prowlarr: each tracker's query limit, and what it was asked over that time (every search and RSS poll, whoever asked). Adding's searches stop short of a limit, keeping a fifth of it for Radarr's and Sonarr's RSS. A tracker without a limit isn't paced: set one in Prowlarr (the tracker's settings, Query Limit).">
    {#if data.error}<Notice warn>{data.error}</Notice>{/if}
    {#if data.trackers.length}
      <table>
        <thead><tr><th>Tracker</th><th>Limit</th><th class="num">Asked</th><th>Searches left</th></tr></thead>
        <tbody>
          {#each data.trackers as t (t.name)}
            <tr>
              <td><span class="name">{t.name}</span></td>
              <td>{#if t.limit}{t.limit} {t.per === 'hour' ? 'an hour' : 'a day'}{:else}<span class="muted">None</span>{/if}</td>
              <td class="num">{t.used}</td>
              <td>{#if t.limit}{#if t.searches > 0}{t.searches}{:else}<Badge kind="warn">None now</Badge>{/if}{:else}<span class="muted">Not paced</span>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </Card>
{/if}
