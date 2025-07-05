import type { User } from '@/api/users'

type UserSearchUser = Pick<User, 'username' | 'email' | 'role' | 'groups' | 'home_dir'>

function normalizeSearchText(value: string | undefined): string {
  return (value ?? '').trim().toLowerCase()
}

function getUserSearchFields(user: UserSearchUser): string[] {
  return [
    user.username,
    user.email,
    user.role,
    user.home_dir,
    ...(user.groups ?? []),
  ]
}

export function userMatchesSearchQuery(user: UserSearchUser, query: string): boolean {
  const normalizedQuery = normalizeSearchText(query)
  if (!normalizedQuery) {
    return true
  }

  return getUserSearchFields(user)
    .map(normalizeSearchText)
    .some((field) => field.includes(normalizedQuery))
}

export function filterUsersBySearchQuery<T extends UserSearchUser>(users: T[], query: string): T[] {
  return users.filter((user) => userMatchesSearchQuery(user, query))
}
