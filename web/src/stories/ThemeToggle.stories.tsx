import type { Meta, StoryObj } from '@storybook/react'
import { HeroUIProvider } from '@heroui/react'
import { ThemeToggle } from '../components/ThemeToggle'

const meta: Meta<typeof ThemeToggle> = {
  title: 'Components/ThemeToggle',
  component: ThemeToggle,
  parameters: {
    layout: 'centered',
  },
  decorators: [
    (Story) => (
      <HeroUIProvider>
        <div className="p-4">
          <Story />
        </div>
      </HeroUIProvider>
    ),
  ],
  tags: ['autodocs'],
}

export default meta
type Story = StoryObj<typeof meta>

/**
 * 默认状态 - 主题切换按钮
 */
export const Default: Story = {}

/**
 * 在深色背景上
 */
export const OnDarkBackground: Story = {
  decorators: [
    (Story) => (
      <HeroUIProvider>
        <div className="p-8 bg-gray-900 rounded-lg">
          <Story />
        </div>
      </HeroUIProvider>
    ),
  ],
}

/**
 * 在浅色背景上
 */
export const OnLightBackground: Story = {
  decorators: [
    (Story) => (
      <HeroUIProvider>
        <div className="p-8 bg-white rounded-lg">
          <Story />
        </div>
      </HeroUIProvider>
    ),
  ],
}
