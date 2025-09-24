import { fireEvent, render, screen } from '@testing-library/react'
import { HardDrive } from 'lucide-react'
import { describe, expect, it, vi } from 'vitest'
import { StatCard } from './StatCard'

describe('StatCard', () => {
  it('renders the compact form without optional regions', () => {
    const { container } = render(<StatCard title="Files" value={42} className="custom-card" />)

    expect(screen.getByText('Files')).toBeInTheDocument()
    expect(screen.getByText('42')).toBeInTheDocument()
    expect(screen.queryByText('Manage')).not.toBeInTheDocument()
    expect(container.firstElementChild).toHaveClass('card-meridian', 'custom-card')
  })

  it('renders icon, subtitle, action, and tone styling when provided', () => {
    render(
      <StatCard
        title="Storage"
        value="8 GB"
        subtitle="of 10 GB"
        icon={HardDrive}
        tone="warning"
        action={<button type="button">Manage</button>}
      />
    )

    expect(screen.getByText('Storage')).toBeInTheDocument()
    expect(screen.getByText('8 GB')).toBeInTheDocument()
    expect(screen.getByText('of 10 GB')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Manage' })).toBeInTheDocument()
    expect(document.querySelector('svg')?.parentElement).toHaveClass('bg-warning/10', 'text-warning')
  })

  it('supports an optional press action', () => {
    const onPress = vi.fn()
    const { container } = render(<StatCard title="Warnings" value={3} onPress={onPress} ariaLabel="查看告警" />)

    const button = screen.getByRole('button', { name: '查看告警' })
    expect(button).toHaveClass('min-h-[6rem]')

    fireEvent.click(button)

    expect(onPress).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[class*="hover:border-primary/40"]')).toBeInTheDocument()
  })

  it('uses visible stat content as the default accessible name for pressable cards', () => {
    const onPress = vi.fn()
    render(<StatCard title="Storage" value="8 GB" subtitle="80% used" onPress={onPress} />)

    expect(screen.getByRole('button', { name: 'Storage，8 GB，80% used' })).toBeInTheDocument()
  })

  it('supports compact density for dense mobile stat grids', () => {
    const { container } = render(<StatCard title="Users" value={6} subtitle="2 need review" icon={HardDrive} density="compact" />)

    expect(screen.getByText('Users')).toHaveClass('text-[11px]')
    expect(screen.getByText('6')).toHaveClass('text-xl')
    expect(screen.getByText('2 need review')).toHaveClass('text-[11px]')
    expect(document.querySelector('svg')?.parentElement).toHaveClass('h-8', 'w-8', 'self-start')
    expect(container.querySelector('[class*="min-h-[5rem]"]')).toBeInTheDocument()
  })
})
