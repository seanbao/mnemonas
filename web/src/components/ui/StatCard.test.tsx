import { render, screen } from '@testing-library/react'
import { HardDrive } from 'lucide-react'
import { describe, expect, it } from 'vitest'
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
})
