const { heroui } = require('@heroui/theme')

const lightDefault = {
  50: '#f8fafc',
  100: '#f1f5f9',
  200: '#e2e8f0',
  300: '#cbd5e1',
  400: '#94a3b8',
  500: '#64748b',
  600: '#475569',
  700: '#334155',
  800: '#1e293b',
  900: '#0f172a',
  DEFAULT: '#64748b',
  foreground: '#111827',
}

const darkDefault = {
  50: '#0f172a',
  100: '#172237',
  200: '#25314a',
  300: '#334155',
  400: '#64748b',
  500: '#94a3b8',
  600: '#cbd5e1',
  700: '#e2e8f0',
  800: '#f1f5f9',
  900: '#f8fafc',
  DEFAULT: '#94a3b8',
  foreground: '#e5e7eb',
}

const primary = {
  50: '#eef2ff',
  100: '#e0e7ff',
  200: '#c7d2fe',
  300: '#a5b4fc',
  400: '#8b9cfb',
  500: '#4f6fdb',
  600: '#4260cb',
  700: '#3553b5',
  800: '#304793',
  900: '#2b3d77',
  DEFAULT: '#4f6fdb',
  foreground: '#ffffff',
}

const secondary = {
  50: '#ecfeff',
  100: '#cffafe',
  200: '#a5f3fc',
  300: '#67e8f9',
  400: '#38d5f1',
  500: '#14b8a6',
  600: '#0d9488',
  700: '#0f766e',
  800: '#115e59',
  900: '#134e4a',
  DEFAULT: '#14b8a6',
  foreground: '#ffffff',
}

const success = {
  50: '#ecfdf5',
  100: '#d1fae5',
  200: '#a7f3d0',
  300: '#6ee7b7',
  400: '#34d399',
  500: '#10b981',
  600: '#059669',
  700: '#047857',
  800: '#065f46',
  900: '#064e3b',
  DEFAULT: '#10b981',
  foreground: '#ffffff',
}

const warning = {
  50: '#fffbeb',
  100: '#fef3c7',
  200: '#fde68a',
  300: '#fcd34d',
  400: '#fbbf24',
  500: '#f59e0b',
  600: '#d97706',
  700: '#b45309',
  800: '#92400e',
  900: '#78350f',
  DEFAULT: '#f59e0b',
  foreground: '#ffffff',
}

const danger = {
  50: '#fff1f2',
  100: '#ffe4e6',
  200: '#fecdd3',
  300: '#fda4af',
  400: '#fb7185',
  500: '#f43f5e',
  600: '#e11d48',
  700: '#be123c',
  800: '#9f1239',
  900: '#881337',
  DEFAULT: '#f43f5e',
  foreground: '#ffffff',
}

module.exports = heroui({
  defaultTheme: 'light',
  layout: {
    radius: {
      small: '6px',
      medium: '8px',
      large: '8px',
    },
    boxShadow: {
      small: '0 3px 12px rgba(15, 23, 42, 0.07)',
      medium: '0 8px 22px rgba(15, 23, 42, 0.1)',
      large: '0 14px 32px rgba(15, 23, 42, 0.14)',
    },
  },
  themes: {
    light: {
      colors: {
        background: '#f7f8fb',
        foreground: '#111827',
        divider: 'rgba(17, 24, 39, 0.08)',
        focus: '#4f6fdb',
        overlay: '#000000',
        content1: {
          DEFAULT: '#ffffff',
          foreground: '#111827',
        },
        content2: {
          DEFAULT: '#e9edf5',
          foreground: '#111827',
        },
        content3: {
          DEFAULT: '#eef2f7',
          foreground: '#334155',
        },
        content4: {
          DEFAULT: '#e5e9f2',
          foreground: '#334155',
        },
        default: lightDefault,
        primary,
        secondary,
        success,
        warning,
        danger,
      },
    },
    dark: {
      colors: {
        background: '#0f172a',
        foreground: '#e5e7eb',
        divider: 'rgba(148, 163, 184, 0.18)',
        focus: '#4f6fdb',
        overlay: '#000000',
        content1: {
          DEFAULT: '#111827',
          foreground: '#e5e7eb',
        },
        content2: {
          DEFAULT: '#172237',
          foreground: '#e5e7eb',
        },
        content3: {
          DEFAULT: '#1f2a40',
          foreground: '#cbd5e1',
        },
        content4: {
          DEFAULT: '#25314a',
          foreground: '#cbd5e1',
        },
        default: darkDefault,
        primary,
        secondary,
        success,
        warning,
        danger,
      },
    },
  },
})
