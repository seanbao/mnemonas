import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { createUser, deleteUser, listUsers, resetUserPassword, toggleUserStatus } from './users'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const mockAuthFetch = authFetch as Mock

describe('Users API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('unwraps wrapped list users responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          users: [{ id: 'u1', username: 'admin', email: '', role: 'admin', disabled: false, home_dir: '/', created_at: '2024-01-01', updated_at: '2024-01-01', quota_bytes: 0, used_bytes: 0 }],
          total: 1,
        },
      }),
    })

    const result = await listUsers()

    expect(result.users).toHaveLength(1)
    expect(result.total).toBe(1)
  })

  it('unwraps wrapped create user responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: { id: 'u1', username: 'admin', email: '', role: 'admin', disabled: false, home_dir: '/', created_at: '2024-01-01', updated_at: '2024-01-01', quota_bytes: 0, used_bytes: 0 },
        },
      }),
    })

    const result = await createUser({ username: 'admin', password: 'password123' })

    expect(result.user.username).toBe('admin')
  })

  it('uses structured error messages', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ success: false, error: { message: 'user already exists', code: 'USER_EXISTS' } }),
    })

    await expect(createUser({ username: 'admin', password: 'password123' })).rejects.toThrow('user already exists')
  })

  it('maps wrapped success for delete, reset password, and toggle status', async () => {
    mockAuthFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true, message: 'user deleted successfully' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true, message: 'password reset successfully' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true, data: { disabled: true }, message: 'user status updated successfully' }) })

    await expect(deleteUser('u1')).resolves.toEqual({ success: true })
    await expect(resetUserPassword('u1', { new_password: 'password123' })).resolves.toEqual({ success: true })
    await expect(toggleUserStatus('u1', true)).resolves.toEqual({ success: true })
  })
})