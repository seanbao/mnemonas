import { describe, expect, it } from 'vitest'
import {
  GENERIC_ACTION_ERROR_DESCRIPTION,
  GENERIC_LOAD_ERROR_DESCRIPTION,
  INVALID_API_RESPONSE_MESSAGE,
  getUserFacingErrorDescription,
} from './apiMessages'

describe('apiMessages', () => {
  it('uses a generic action description instead of arbitrary Error messages', () => {
    expect(getUserFacingErrorDescription(new Error('backend timeout'))).toBe(GENERIC_ACTION_ERROR_DESCRIPTION)
  })

  it('supports load-specific fallback descriptions', () => {
    expect(getUserFacingErrorDescription(new Error('Network error'), GENERIC_LOAD_ERROR_DESCRIPTION))
      .toBe(GENERIC_LOAD_ERROR_DESCRIPTION)
  })

  it('preserves the local invalid API response message', () => {
    expect(getUserFacingErrorDescription(new Error(INVALID_API_RESPONSE_MESSAGE))).toBe(INVALID_API_RESPONSE_MESSAGE)
  })
})
