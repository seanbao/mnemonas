export function shouldValidateSessionOnInitialRoute(pathname: string): boolean {
  return pathname !== '/login' && pathname !== '/s' && !pathname.startsWith('/s/')
}
