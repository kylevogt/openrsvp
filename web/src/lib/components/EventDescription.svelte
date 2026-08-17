<script lang="ts">
	interface Props {
		description: string;
		/** Lines shown before the text is clamped. */
		lines?: number;
		class?: string;
	}

	let { description, lines = 3, class: className = '' }: Props = $props();

	let expanded = $state(false);
	let clamped = $state(false);
	let textEl = $state<HTMLElement | null>(null);
	const textId = $props.id();

	// Only measure while collapsed: expanding removes the clamp, so the element
	// stops overflowing and the toggle would vanish under the user's cursor.
	function measure() {
		if (!textEl || expanded) return;
		clamped = textEl.scrollHeight - textEl.clientHeight > 1;
	}

	$effect(() => {
		// Re-run when the text, clamp height, or element itself changes.
		void description;
		void lines;
		void textEl;
		measure();
	});

	$effect(() => {
		const el = textEl;
		if (!el || typeof ResizeObserver === 'undefined') return;
		// Reflow (window resize, font load) changes how many lines the text takes.
		const observer = new ResizeObserver(() => measure());
		observer.observe(el);
		return () => observer.disconnect();
	});
</script>

{#if description.trim()}
	<div class={className}>
		<p
			bind:this={textEl}
			id={textId}
			class="description text-sm leading-[1.5] text-neutral-700 whitespace-pre-wrap"
			class:clamped={!expanded}
			class:overflows={clamped}
			style="--description-lines: {lines}"
		>{description}</p>
		{#if clamped}
			<button
				type="button"
				onclick={() => (expanded = !expanded)}
				aria-expanded={expanded}
				aria-controls={textId}
				class="mt-2 inline-flex items-center gap-1 text-sm font-medium text-primary hover:text-primary-hover transition-colors duration-short ease-out focus:outline-none focus:ring-2 focus:ring-primary/40 rounded-sm"
			>
				{expanded ? 'Show less' : 'Show more'}
				<svg
					class="w-4 h-4 transition-transform duration-short ease-out"
					class:rotate-180={expanded}
					fill="none"
					viewBox="0 0 24 24"
					stroke="currentColor"
					stroke-width="2"
					aria-hidden="true"
				>
					<path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
				</svg>
			</button>
		{/if}
	</div>
{/if}

<style>
	/* Fixed line-height so the clamp height below is exact, and so expanding
	   doesn't reflow the text that was already visible. */
	.description {
		--description-line-height: 1.5;
		line-height: var(--description-line-height);
	}
	/* Height clamp stays on while collapsed so we can measure overflow.
	   The fade is only applied when the text actually overflows: short
	   descriptions would otherwise look truncated with no "Show more".
	   A height clamp with a fade rather than -webkit-line-clamp, because
	   descriptions often contain blank lines between paragraphs, and the
	   line-clamp ellipsis lands on one of those as a stray "..." on its
	   own line. */
	.clamped {
		max-height: calc(var(--description-lines, 3) * var(--description-line-height) * 1em);
		overflow: hidden;
	}
	.clamped.overflows {
		mask-image: linear-gradient(to bottom, #000 60%, transparent 100%);
		-webkit-mask-image: linear-gradient(to bottom, #000 60%, transparent 100%);
	}
</style>
