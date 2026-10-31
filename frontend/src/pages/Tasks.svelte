<!-- Tasks: Overtide's scheduled work, when each task last ran and runs
     next; running one now. -->
<script lang="ts">
  import '../styles/library.css';
  import { PageHead, Busy, Notice, Loading } from '@overfin/ui';
  import { get, post } from '../lib/api';
  import { follow } from '../lib/live.svelte';
  import type { Task, Workflow } from '../lib/types-library';
  import { stamp } from '../lib/types-library';

  let data = $state<{ groups: { name: string; tasks: Task[] }[]; workflows: Workflow[] } | null>(null);
  let problem = $state('');

  async function load() {
    try {
      data = await get('/api/tasks');
      problem = '';
    } catch (err) {
      problem = (err as Error).message;
    }
  }

  $effect(() => {
    load();
    return follow('immediate', load);
  });

  async function run(t: Task) {
    await post('/api/tasks/run', { id: t.id });
    await load();
  }
</script>

<PageHead title="Tasks" description="Overtide's scheduled work: when each task last ran, and when it runs next." />
{#if problem}<Notice warn>{problem}</Notice>{/if}

{#if !data}
  {#if !problem}<Loading />{/if}
{:else}
  {#each data.groups as g (g.name)}
    <h2>{g.name}</h2>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Task</th><th>Schedule</th><th>Last run</th><th>Duration</th><th>Next run</th><th>Status</th><th><span class="visually-hidden">Actions</span></th></tr></thead>
        <tbody>
          {#each g.tasks as t (t.id)}
            <tr>
              <td><b>{t.name}</b><br /><span class="muted">{t.description}</span></td>
              <td>{t.schedule}</td>
              <td class="nowrap">{stamp(t.lastFinished)}</td>
              <td>{t.duration}</td>
              <td class="nowrap">{t.scheduled ? stamp(t.nextRun) : '—'}</td>
              <td>
                <strong>{t.state}</strong>
                {#if t.priority}<br /><span class="muted">{t.priority} priority</span>{/if}
                {#if t.waiting}<br /><span class="muted">{t.waiting}</span>{/if}
                {#if t.lastError}<br /><span class="error">{t.lastError}</span>{:else if t.lastWarning}<br /><span class="dry">{t.lastWarning}</span>{/if}
              </td>
              <td class="row-actions">
                {#if t.canRun}<Busy small disabled={t.busy} action={() => run(t)}>Run now</Busy>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/each}

  {#if data.workflows.length}
    <h2>Consistency workflows</h2>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Workflow</th><th>Revision</th><th>Progress</th><th>Status</th><th>Updated</th></tr></thead>
        <tbody>
          {#each data.workflows as w, i (i)}
            <tr>
              <td><b>{w.name}</b><br /><span class="muted">{w.description}</span></td>
              <td>{w.revision}</td>
              <td>
                {#if w.state === 'succeeded'}{w.totalSteps} / {w.totalSteps}{:else}{w.currentStep} / {w.totalSteps}{#if w.stepName}<br /><span class="muted">Next: {w.stepName}</span>{/if}{/if}
              </td>
              <td>{w.state}</td>
              <td class="nowrap">{stamp(w.updatedAt)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
{/if}
