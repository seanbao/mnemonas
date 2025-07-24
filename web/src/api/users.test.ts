import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { createUser, deleteUser, listUsers, resetUserPassword, toggleUserStatus, UsersError } from './users'

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

  it('derives total users from the returned list when the summary field is missing', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          users: [
            { id: 'u1', username: 'admin', email: '', role: 'admin', disabled: false, home_dir: '/', created_at: '2024-01-01', updated_at: '2024-01-01', quota_bytes: 0, used_bytes: 0 },
            { id: 'u2', username: 'guest', email: '', role: 'guest', disabled: false, home_dir: '/guest', created_at: '2024-01-01', updated_at: '2024-01-01', quota_bytes: 0, used_bytes: 0 },
          ],
        },
      }),
    })

    const result = await listUsers()

    expect(result.users).toHaveLength(2)
    expect(result.total).toBe(2)
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

  it('rejects malformed successful list users responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          users: [{ id: 'u1', username: 'admin', email: '', role: 'superadmin', disabled: false, home_dir: '/', created_at: '2024-01-01', updated_at: '2024-01-01', quota_bytes: 0, used_bytes: 0 }],
        },
      }),
    })

    await expect(listUsers()).rejects.toThrow('Invalid users response')
  })

  it('rejects list users responses with non-object user entries', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          users: [null],
        },
      }),
    })

    await expect(listUsers()).rejects.toThrow('Invalid users response')
  })

  it('rejects unreadable successful list users responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(listUsers()).rejects.toThrow('Invalid users response')
  })

  it('rejects malformed successful create user responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: { id: 'u1', username: 'admin', email: '', role: 'admin', disabled: false, created_at: '2024-01-01', updated_at: '2024-01-01', quota_bytes: 0, used_bytes: 0 },
        },
      }),
    })

    await expect(createUser({ username: 'admin', password: 'password123' })).rejects.toThrow('Invalid create user response')
  })

  it('uses structured error messages', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 409,
      json: () => Promise.resolve({ success: false, error: { message: 'user already exists', code: 'USER_EXISTS' } }),
    })

    await expect(createUser({ username: 'admin', password: 'password123' })).rejects.toThrow('user already exists')
  })

  it('preserves unavailable users API metadata', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ success: false, error: { message: 'configuration not available' } }),
    })

    try {
      await listUsers()
      throw new Error('Expected listUsers to throw')
    } catch (error) {
      expect(error).toBeInstanceOf(UsersError)
      expect((error as UsersError).message).toBe('configuration not available')
      expect((error as UsersError).status).toBe(503)
      expect((error as UsersError).isUnavailable).toBe(true)
    }
  })

  it('falls back to top-level message when structured error is absent', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      json: () => Promise.resolve({ success: false, message: 'cannot delete current user' }),
    })

    await expect(deleteUser('u1')).rejects.toThrow('cannot delete current user')
  })

  it('falls back to generic error when error body is invalid', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(toggleUserStatus('u1', true)).rejects.toThrow('Failed to update user status')
  })

  it('uses the reset password fallback when its error body is invalid', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(resetUserPassword('u1', { new_password: 'password123' })).rejects.toThrow('Failed to reset password')
  })

  it('maps wrapped success for delete, reset password, and toggle status', async () => {
    mockAuthFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true, data: null, message: 'user deleted successfully' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true, data: null, message: 'password reset successfully' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true, data: { disabled: true }, message: 'user status updated successfully' }) })

    await expect(deleteUser('u1')).resolves.toEqual({ success: true, warning: false, message: 'user deleted successfully' })
    await expect(resetUserPassword('u1', { new_password: 'password123' })).resolves.toEqual({ success: true, warning: false, message: 'password reset successfully' })
    await expect(toggleUserStatus('u1', true)).resolves.toEqual({ success: true, warning: false, message: 'user status updated successfully' })
  })

  it('preserves warning metadata for wrapped user management success responses', async () => {
    mockAuthFetch
      .mockResolvedValueOnce({
        ok: true,
        headers: { get: () => '199 auth persistence warning' },
        json: () => Promise.resolve({
          success: true,
          message: 'user created with persistence warning',
          data: {
            user: { id: 'u1', username: 'admin', email: '', role: 'admin', disabled: false, home_dir: '/', created_at: '2024-01-01', updated_at: '2024-01-01', quota_bytes: 0, used_bytes: 0 },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        headers: { get: () => '199 auth persistence warning' },
        json: () => Promise.resolve({ success: true, data: null, message: 'user deleted with persistence warning' }),
      })
      .mockResolvedValueOnce({
        ok: true,
        headers: { get: () => null },
        json: () => Promise.resolve({ success: true, data: { warning: true }, message: 'password reset with persistence warning' }),
      })
      .mockResolvedValueOnce({
        ok: true,
        headers: { get: () => null },
        json: () => Promise.resolve({ success: true, data: { disabled: true, warning: true }, message: 'user status updated with persistence warning' }),
      })

    await expect(createUser({ username: 'admin', password: 'password123' })).resolves.toMatchObject({
      success: true,
      warning: true,
      message: 'user created with persistence warning',
    })
    await expect(deleteUser('u1')).resolves.toEqual({
      success: true,
      warning: true,
      message: 'user deleted with persistence warning',
    })
    await expect(resetUserPassword('u1', { new_password: 'password123' })).resolves.toEqual({
      success: true,
      warning: true,
      message: 'password reset with persistence warning',
    })
    await expect(toggleUserStatus('u1', true)).resolves.toEqual({
      success: true,
      warning: true,
      message: 'user status updated with persistence warning',
    })
  })

  it('rejects malformed successful delete/reset/toggle responses instead of treating them as success', async () => {
    mockAuthFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true, message: 'user deleted successfully' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true, message: 'password reset successfully' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: false, data: { disabled: true } }) })

    await expect(deleteUser('u1')).rejects.toThrow('Invalid delete user response')
    await expect(resetUserPassword('u1', { new_password: 'password123' })).rejects.toThrow('Invalid reset password response')
    await expect(toggleUserStatus('u1', true)).rejects.toThrow('Invalid update user status response')
  })

  it('rejects toggle status success responses that omit the disabled field', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ success: true, data: {} }),
    })

    await expect(toggleUserStatus('u1', true)).rejects.toThrow('Invalid update user status response')
  })
})
