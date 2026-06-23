<script lang="ts">
  import type { Snippet } from 'svelte';
  
  interface Props {
    type?: 'button' | 'submit';
    variant?: 'primary' | 'secondary' | 'ghost';
    onclick?: (e: MouseEvent) => void;
    disabled?: boolean;
    class?: string;
    children: Snippet;
  }
  
  let { 
    type = 'button', 
    variant = 'primary', 
    onclick, 
    disabled = false, 
    class: className = '', 
    children 
  }: Props = $props();

  const variantClasses = {
    primary: 'bg-primary text-canvas hover:bg-[#D5D2C9] border-transparent font-bold',
    secondary: 'bg-transparent text-primary hover:bg-white/[0.02] border-white/10 font-semibold',
    ghost: 'bg-transparent text-muted hover:text-primary hover:bg-white/[0.02] border-transparent font-medium'
  };
</script>

<button
  {type}
  {disabled}
  {onclick}
  class="w-full inline-flex items-center justify-center rounded-full border px-6 py-3.5 text-xs font-manrope uppercase tracking-widestUI transition-all duration-150 active:scale-[0.98] disabled:opacity-40 disabled:pointer-events-none select-none {variantClasses[variant]} {className}"
>
  {#if children}
    {@render children()}
  {/if}
</button>
