import { describe, expect, it } from 'vitest'
import fc from 'fast-check'
import { decodePathFromUrl, encodePathForUrl, hasControlCharacter, normalizePath, pathWithinBase } from './utils'

const propertyOptions = {
  numRuns: 300,
  seed: 20260504,
}

const urlSegmentArbitrary = fc
  .string({ maxLength: 16, unit: 'grapheme' })
  .filter((segment) => !segment.includes('/') && !hasControlCharacter(segment))

const safeSegmentArbitrary = fc
  .string({ minLength: 1, maxLength: 16, unit: 'grapheme' })
  .filter((segment) => (
    !segment.includes('/') &&
    !hasControlCharacter(segment) &&
    segment !== '.' &&
    segment !== '..'
  ))

const safePathSegmentsArbitrary = fc.array(safeSegmentArbitrary, { maxLength: 6 })

function toAbsolutePath(segments: string[]): string {
  return segments.length === 0 ? '/' : `/${segments.join('/')}`
}

describe('path helpers properties', () => {
  it('round-trips URL path encoding by segment', () => {
    fc.assert(
      fc.property(fc.array(urlSegmentArbitrary, { maxLength: 6 }), (segments) => {
        const path = toAbsolutePath(segments)

        expect(decodePathFromUrl(encodePathForUrl(path))).toBe(path)
      }),
      propertyOptions
    )
  })

  it('normalizes safe paths to a stable absolute representation', () => {
    fc.assert(
      fc.property(safePathSegmentsArbitrary, fc.boolean(), fc.boolean(), (segments, absolute, trailingSlash) => {
        let input = segments.join('//')
        if (absolute) {
          input = `/${input}`
        }
        if (trailingSlash) {
          input = `${input}/`
        }

        const normalized = normalizePath(input)

        expect(normalized).toMatch(/^\//)
        expect(normalized).not.toMatch(/\/{2,}/)
        expect(normalizePath(normalized)).toBe(normalized)
        if (normalized !== '/') {
          expect(normalized.endsWith('/')).toBe(false)
        }
      }),
      propertyOptions
    )
  })

  it('rejects any slash-delimited parent traversal segment', () => {
    fc.assert(
      fc.property(safePathSegmentsArbitrary, safePathSegmentsArbitrary, (prefixSegments, suffixSegments) => {
        const input = [...prefixSegments, '..', ...suffixSegments].join('/')

        expect(() => normalizePath(input)).toThrow('非法路径')
      }),
      propertyOptions
    )
  })

  it('keeps base path checks segment-boundary aware', () => {
    fc.assert(
      fc.property(
        safePathSegmentsArbitrary,
        fc.array(safeSegmentArbitrary, { maxLength: 4 }),
        (baseSegments, childSegments) => {
          const basePath = toAbsolutePath(baseSegments)
          const childPath = childSegments.length === 0
            ? basePath
            : basePath === '/'
              ? toAbsolutePath(childSegments)
              : `${basePath}/${childSegments.join('/')}`

          expect(pathWithinBase(basePath, childPath)).toBe(true)
        }
      ),
      propertyOptions
    )

    fc.assert(
      fc.property(
        fc.array(safeSegmentArbitrary, { minLength: 1, maxLength: 6 }),
        safeSegmentArbitrary,
        (baseSegments, siblingSuffix) => {
          const basePath = toAbsolutePath(baseSegments)
          const siblingPath = `${basePath}${siblingSuffix}`

          expect(pathWithinBase(basePath, siblingPath)).toBe(false)
        }
      ),
      propertyOptions
    )
  })
})
