import { describe, expect, it } from 'vitest'
import {
  INVALID_FOLDER_UPLOAD_PATH_MESSAGE,
  buildUploadQueue,
  getUploadPanelTitle,
  getUploadQueueCounts,
  normalizeFolderUploadRelativePath,
  normalizeUploadProgress,
  type UploadQueueItem,
} from './uploadQueue'

const queueOptions = {
  maxUploadFileSizeBytes: 1024,
  maxUploadFileSizeLabel: '1 KB',
}

function createFile(name: string, size = 4): File {
  const file = new File(['x'], name, { type: 'text/plain' })
  Object.defineProperty(file, 'size', {
    configurable: true,
    value: size,
  })
  return file
}

function setFolderRelativePath(file: File, relativePath: string): File {
  Object.defineProperty(file, 'webkitRelativePath', {
    configurable: true,
    value: relativePath,
  })
  return file
}

describe('uploadQueue', () => {
  it('normalizes valid folder upload paths', () => {
    expect(normalizeFolderUploadRelativePath('photos/2026/report.txt')).toBe('photos/2026/report.txt')
    expect(normalizeFolderUploadRelativePath('photos\\2026\\report.txt')).toBe('photos/2026/report.txt')
  })

  it('rejects unsafe folder upload paths', () => {
    expect(normalizeFolderUploadRelativePath('/absolute/report.txt')).toBeNull()
    expect(normalizeFolderUploadRelativePath('photos/../secret.txt')).toBeNull()
    expect(normalizeFolderUploadRelativePath('photos//secret.txt')).toBeNull()
    expect(normalizeFolderUploadRelativePath('photos/./secret.txt')).toBeNull()
    expect(normalizeFolderUploadRelativePath('photos/report\0.txt')).toBeNull()
  })

  it('builds pending queue entries for regular and valid folder files', () => {
    const regularFile = createFile('report.txt')
    const folderFile = setFolderRelativePath(createFile('photo.jpg'), 'album/photo.jpg')

    const queue = buildUploadQueue([regularFile, folderFile], queueOptions)

    expect(queue).toMatchObject([
      {
        file: regularFile,
        relativePath: 'report.txt',
        folderRelativePath: undefined,
        progress: 0,
        status: 'pending',
        error: undefined,
      },
      {
        file: folderFile,
        relativePath: 'album/photo.jpg',
        folderRelativePath: 'album/photo.jpg',
        progress: 0,
        status: 'pending',
        error: undefined,
      },
    ])
  })

  it('rejects oversized files before upload', () => {
    const file = createFile('huge.bin', 2048)

    const queue = buildUploadQueue([file], queueOptions)

    expect(queue[0]).toMatchObject({
      relativePath: 'huge.bin',
      progress: 0,
      status: 'error',
      error: 'huge.bin 超过 1 KB 上传限制',
    })
  })

  it('rejects invalid folder paths before upload', () => {
    const file = setFolderRelativePath(createFile('secret.txt'), 'photos/../secret.txt')

    const queue = buildUploadQueue([file], queueOptions)

    expect(queue[0]).toMatchObject({
      relativePath: 'secret.txt',
      folderRelativePath: undefined,
      progress: 0,
      status: 'error',
      error: INVALID_FOLDER_UPLOAD_PATH_MESSAGE,
    })
  })

  it('clamps non-finite and out-of-range progress values', () => {
    expect(normalizeUploadProgress(Number.NaN)).toBe(0)
    expect(normalizeUploadProgress(Number.POSITIVE_INFINITY)).toBe(0)
    expect(normalizeUploadProgress(-12)).toBe(0)
    expect(normalizeUploadProgress(42.5)).toBe(42.5)
    expect(normalizeUploadProgress(150)).toBe(100)
  })

  it('counts terminal upload states for summary display', () => {
    const queue = [
      { status: 'pending' },
      { status: 'uploading' },
      { status: 'done' },
      { status: 'error' },
      { status: 'cancelled' },
    ] as UploadQueueItem[]

    expect(getUploadQueueCounts(queue)).toEqual({
      total: 5,
      done: 1,
      error: 1,
      cancelled: 1,
      finished: 3,
    })
  })

  it('derives upload panel titles from upload state and counts', () => {
    expect(getUploadPanelTitle(true, { total: 3, done: 1, error: 1, cancelled: 0, finished: 2 }))
      .toBe('上传中 (2/3)')
    expect(getUploadPanelTitle(false, { total: 1, done: 0, error: 0, cancelled: 1, finished: 1 }))
      .toBe('上传已取消')
    expect(getUploadPanelTitle(false, { total: 1, done: 0, error: 1, cancelled: 0, finished: 1 }))
      .toBe('上传失败')
    expect(getUploadPanelTitle(false, { total: 2, done: 1, error: 1, cancelled: 0, finished: 2 }))
      .toBe('上传部分完成')
    expect(getUploadPanelTitle(false, { total: 1, done: 1, error: 0, cancelled: 0, finished: 1 }))
      .toBe('上传完成')
  })
})
