import { Button } from '@heroui/react'
import { Moon, Sun, Monitor } from 'lucide-react'
import { useThemeStore } from '@/stores/theme'

export function ThemeToggle() {
  const { theme, setTheme } = useThemeStore()

  const cycleTheme = () => {
    const themes: Array<'light' | 'dark' | 'system'> = ['light', 'dark', 'system']
    const currentIndex = themes.indexOf(theme)
    const nextTheme = themes[(currentIndex + 1) % themes.length]
    setTheme(nextTheme)
  }

  const Icon = theme === 'light' ? Sun : theme === 'dark' ? Moon : Monitor

  return (
    <Button
      isIconOnly
      variant="light"
      aria-label="Toggle theme"
      onPress={cycleTheme}
      className="rounded-lg text-default-500 hover:text-default-900"
    >
      <Icon size={20} />
    </Button>
  )
}
