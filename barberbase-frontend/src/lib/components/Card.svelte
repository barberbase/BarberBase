<script lang="ts">
  import type { Snippet } from 'svelte';
  
  interface Props {
    class?: string;
    variant?: 'matte' | 'surface' | 'titanium';
    children: Snippet;
  }
  
  let { class: className = '', variant = 'matte', children }: Props = $props();

  const bgClasses = {
    matte: 'bg-matte border-white/[0.03]',
    surface: 'bg-surface border-white/[0.04]',
    titanium: 'bg-titanium border-white/[0.05]'
  };
</script>

<div class="rounded-xl border machined-edge p-5 relative overflow-hidden transition-all duration-200 {bgClasses[variant]} {className}">
  {@render children()}
</div>
