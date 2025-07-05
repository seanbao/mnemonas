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
    render(<StatCard title="Warnings" value={3} onPress={onPress} ariaLabel="查看告警" />)

    fireEvent.click(screen.getByRole('button', { name: '查看告警' }))

    expect(onPress).toHaveBeenCalledTimes(1)
  })

  it('supports compact density for dense mobile stat grids', () => {
    render(<StatCard title="Users" value={6} subtitle="2 need review" icon={HardDrive} density="compact" />)

    expect(screen.getByText('Users')).toHaveClass('text-[11px]')
    expect(screen.getByText('6')).toHaveClass('text-xl')
    expect(screen.getByText('2 need review')).toHaveClass('text-[11px]')
    expect(document.querySelector('svg')?.parentElement).toHaveClass('h-8', 'w-8')
  })
})
