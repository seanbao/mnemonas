import { describe, expect, it } from 'vitest'
import { filterUsersBySearchQuery, userMatchesSearchQuery } from './userSearch'

const users = [
  {
    username: 'alice',
    email: 'alice@example.com',
    role: 'user' as const,
    groups: ['family', 'editors'],
    home_dir: '/home/alice',
  },
  {
    username: 'media',
    email: '',
    role: 'guest' as const,
    groups: ['guests'],
    home_dir: '/shares/media',
  },
  {
    username: 'root-admin',
    email: 'ops@example.com',
    role: 'admin' as const,
    groups: ['ops'],
    home_dir: '/',
  },
]

describe('userSearch', () => {
  it('matches empty queries', () => {
    expect(filterUsersBySearchQuery(users, '').map((user) => user.username)).toEqual([
      'alice',
      'media',
      'root-admin',
    ])
    expect(userMatchesSearchQuery(users[0], '   ')).toBe(true)
  })

  it('matches username, email, role, groups, and home directory case-insensitively', () => {
    expect(filterUsersBySearchQuery(users, 'ALI').map((user) => user.username)).toEqual(['alice'])
    expect(filterUsersBySearchQuery(users, 'ops@example').map((user) => user.username)).toEqual(['root-admin'])
    expect(filterUsersBySearchQuery(users, 'guest').map((user) => user.username)).toEqual(['media'])
    expect(filterUsersBySearchQuery(users, 'EDITORS').map((user) => user.username)).toEqual(['alice'])
    expect(filterUsersBySearchQuery(users, '/shares').map((user) => user.username)).toEqual(['media'])
  })

  it('returns no users when no searchable field matches', () => {
    expect(filterUsersBySearchQuery(users, 'missing')).toEqual([])
  })
})
