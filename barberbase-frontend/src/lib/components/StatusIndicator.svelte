<script lang="ts">
  interface Props {
    state: 'active' | 'success' | 'warning' | 'idle';
  }
  
  let { state }: Props = $props();

  const stateClasses = {
    // The explicit, isolated Champagne Gold accent layer
    active: 'border-gold-accent bg-gold-accent shadow-[0_0_12px_rgba(200,169,107,0.15)] text-canvas',
    success: 'border-system-success bg-system-success/10 text-system-success',
    warning: 'border-system-warning bg-system-warning/10 text-system-warning',
    idle: 'border-white/10 bg-white/5 text-muted'
  };
</script>

<div class="inline-flex items-center gap-2 rounded px-2 py-0.5 text-[10px] font-mono border font-medium {stateClasses[state]}">
  {#if state === 'active'}
    <span class="w-1 h-1 rounded-full bg-canvas animate-pulse"></span>
    <span>YOUR TURN</span>
  {:else if state === 'success'}
    <span class="w-1 h-1 rounded-full bg-system-success"></span>
    <span>PRESENT</span>
  {:else if state === 'warning'}
    <span class="w-1 h-1 rounded-full bg-system-warning"></span>
    <span>ON WAY</span>
  {:else}
    <span class="w-1 h-1 rounded-full bg-muted/40"></span>
    <span>WAITING</span>
  {/if}
</div>
