<script lang="ts">
	import type { PublicGuest } from '$lib/types';

	interface Props {
		guests: PublicGuest[];
		/** Guests shown before the rest are hidden behind a "more" toggle. */
		limit?: number;
		class?: string;
	}

	let { guests, limit = 50, class: className = '' }: Props = $props();

	let showAll = $state(false);
	const shown = $derived(showAll ? guests : guests.slice(0, limit));
</script>

{#if guests.length > 0}
	<div class="flex flex-wrap gap-2 {className}">
		{#each shown as guest}
			<span class="inline-flex items-center gap-1 rounded-full bg-primary-light px-3 py-1 text-xs font-medium text-primary border border-primary-light">
				{guest.name}{#if guest.plusOnes}<span class="opacity-80">+{guest.plusOnes}</span>{/if}
			</span>
		{/each}
		{#if guests.length > limit}
			<button
				type="button"
				class="inline-flex items-center rounded-full bg-neutral-100 px-3 py-1 text-xs font-medium text-neutral-600 hover:bg-neutral-200 transition-colors"
				onclick={() => (showAll = !showAll)}
			>
				{showAll ? 'Show less' : `+${guests.length - limit} more`}
			</button>
		{/if}
	</div>
{/if}
