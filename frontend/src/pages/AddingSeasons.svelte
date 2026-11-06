<!-- Adding, seasons and episodes: whole seasons to replace by a pack, and
     the episodes wanted. -->
<script lang="ts">
  import { PageHead } from '@overfin/ui';
  import AddingGroups from '../parts/adding/AddingGroups.svelte';
</script>

<PageHead title="Seasons & episodes" description="Seasons with episodes missing, replaced by a whole-season pack, and episodes that aired without coming through Sonarr's RSS." />

<AddingGroups states={['seasons', 'episodes']} />
