/** @type {import('tailwindcss').Config} */
export default {
  content: ['./src/**/*.{html,js,svelte,ts}'],
  theme: {
    extend: {
      colors: {
        // Structural Surface Elevation
        canvas: '#080808',     // Deepest level background (OLED safe, no black-smear)
        matte: '#0E0E0E',      // Mid-level structural scaffolding
        surface: '#141414',    // Elevated layer cards and action lists
        titanium: '#1C1C1C',   // Popovers, context fields, inputs

        // Typography Hierarchy (Anti-Halation Muted Values)
        primary: '#E5E2D9',    // Warm Alabaster (Primary texts, readable text nodes)
        muted: '#9F9B93',      // Pebble Grey (Subtext profiles, counters)
        dim: '#5A5854',        // Deep neutral placeholder text vectors

        // Discovered Accents & System States
        gold: {
          accent: '#C8A96B'    // Micro Champagne Gold (Strictly reserved for queue verification highlights)
        },
        system: {
          success: '#10B981',  // Safe desaturated emerald for active timers
          warning: '#F59E0B',  // Low-luminance amber for snoozed actions
          error: '#EF4444'     // Soft structural red for cancellation triggers
        }
      },
      fontFamily: {
        satoshi: ['"Plus Jakarta Sans"', 'sans-serif'], // Bold geometric header locks
        manrope: ['"Inter"', 'sans-serif'],             // Compact UI copy block density
        mono: ['"Space Mono"', 'monospace']             // Strict numeric tabular alignments
      },
      letterSpacing: {
        tightest: '-0.045em',
        widestUI: '0.25em'
      }
    }
  },
  plugins: []
};
