/** Islet Tailwind preset. Colours reference the CSS variables in tokens.css, so
 *  light and dark switch without duplicating utilities.
 *  Usage: presets: [require('../../brand/tokens/tailwind.preset.cjs')] */
module.exports = {
  theme: {
    extend: {
      colors: {
        bg: "var(--islet-bg)",
        surface: { DEFAULT: "var(--islet-surface)", 2: "var(--islet-surface-2)" },
        border: { DEFAULT: "var(--islet-border)", strong: "var(--islet-border-strong)" },
        ink: { DEFAULT: "var(--islet-ink)", muted: "var(--islet-ink-muted)", faint: "var(--islet-ink-faint)" },
        accent: { DEFAULT: "var(--islet-accent)", strong: "var(--islet-accent-strong)", soft: "var(--islet-accent-soft)" },
        success: { DEFAULT: "var(--islet-success)", soft: "var(--islet-success-soft)" },
        warning: { DEFAULT: "var(--islet-warning)", soft: "var(--islet-warning-soft)" },
        danger: { DEFAULT: "var(--islet-danger)", soft: "var(--islet-danger-soft)" },
        code: {
          bg: "var(--islet-code-bg)", fg: "var(--islet-code-fg)", muted: "var(--islet-code-muted)",
          accent: "var(--islet-code-accent)", warn: "var(--islet-code-warn)", error: "var(--islet-code-error)",
        },
      },
      fontFamily: {
        sans: ["Geist", "system-ui", "-apple-system", "Segoe UI", "Roboto", "sans-serif"],
        mono: ["Geist Mono", "ui-monospace", "SFMono-Regular", "Menlo", "Consolas", "monospace"],
      },
      fontSize: {
        xs: ["0.75rem", { lineHeight: "1.45" }],
        sm: ["0.8125rem", { lineHeight: "1.45" }],
        md: ["0.875rem", { lineHeight: "1.45" }],
        base: ["1rem", { lineHeight: "1.6" }],
        lg: ["1.125rem", { lineHeight: "1.4" }],
        xl: ["1.375rem", { lineHeight: "1.15", letterSpacing: "-0.02em" }],
        "2xl": ["1.75rem", { lineHeight: "1.15", letterSpacing: "-0.02em" }],
        "3xl": ["2.75rem", { lineHeight: "1.05", letterSpacing: "-0.03em" }],
      },
      borderRadius: {
        sm: "var(--islet-radius-sm)", md: "var(--islet-radius-md)", lg: "var(--islet-radius-lg)", full: "var(--islet-radius-full)",
      },
      boxShadow: { float: "var(--islet-shadow-float)" },
      transitionDuration: { fast: "var(--islet-duration-fast)", normal: "var(--islet-duration-normal)", slow: "var(--islet-duration-slow)" },
      transitionTimingFunction: { islet: "var(--islet-ease)" },
      ringColor: { DEFAULT: "var(--islet-accent)" },
    },
  },
};
