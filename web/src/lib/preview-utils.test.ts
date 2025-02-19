import { describe, it, expect } from 'vitest'
import {
  getPreviewType,
  buildPreviewUrl,
  getLanguageFromExtension,
  getMimeType,
  canPreview,
  getFileExtension,
} from './preview-utils'

describe('getFileExtension', () => {
  it('extracts extension from filename', () => {
    expect(getFileExtension('file.txt')).toBe('txt')
    expect(getFileExtension('image.PNG')).toBe('png')
  })

  it('returns empty string for no extension', () => {
    expect(getFileExtension('noextension')).toBe('')
    expect(getFileExtension('Makefile')).toBe('')
  })

  it('handles multiple dots', () => {
    expect(getFileExtension('file.name.txt')).toBe('txt')
  })
})

describe('getPreviewType', () => {
  describe('images', () => {
    it('returns image for common image formats', () => {
      expect(getPreviewType('photo.jpg')).toBe('image')
      expect(getPreviewType('photo.jpeg')).toBe('image')
      expect(getPreviewType('image.png')).toBe('image')
      expect(getPreviewType('animation.gif')).toBe('image')
      expect(getPreviewType('icon.webp')).toBe('image')
      expect(getPreviewType('vector.svg')).toBe('image')
      expect(getPreviewType('bitmap.bmp')).toBe('image')
      expect(getPreviewType('icon.ico')).toBe('image')
    })

    it('handles uppercase extensions', () => {
      expect(getPreviewType('photo.JPG')).toBe('image')
      expect(getPreviewType('photo.PNG')).toBe('image')
    })

    it('handles mixed case extensions', () => {
      expect(getPreviewType('photo.JpG')).toBe('image')
    })
  })

  describe('videos', () => {
    it('returns video for common video formats', () => {
      expect(getPreviewType('movie.mp4')).toBe('video')
      expect(getPreviewType('movie.webm')).toBe('video')
      expect(getPreviewType('movie.ogg')).toBe('video')
      expect(getPreviewType('movie.mov')).toBe('video')
      expect(getPreviewType('movie.avi')).toBe('video')
      expect(getPreviewType('movie.mkv')).toBe('video')
    })
  })

  describe('audio', () => {
    it('returns audio for common audio formats', () => {
      expect(getPreviewType('song.mp3')).toBe('audio')
      expect(getPreviewType('song.wav')).toBe('audio')
      expect(getPreviewType('song.flac')).toBe('audio')
      expect(getPreviewType('song.aac')).toBe('audio')
      expect(getPreviewType('song.m4a')).toBe('audio')
    })
  })

  describe('PDF', () => {
    it('returns pdf for PDF files', () => {
      expect(getPreviewType('document.pdf')).toBe('pdf')
    })

    it('handles case insensitivity', () => {
      expect(getPreviewType('document.PDF')).toBe('pdf')
    })
  })

  describe('markdown files', () => {
    it('returns markdown for markdown files', () => {
      expect(getPreviewType('readme.md')).toBe('markdown')
      expect(getPreviewType('docs.markdown')).toBe('markdown')
    })
  })

  describe('text files', () => {
    it('returns text for common text formats', () => {
      expect(getPreviewType('notes.txt')).toBe('text')
      expect(getPreviewType('data.json')).toBe('text')
      expect(getPreviewType('config.xml')).toBe('text')
      expect(getPreviewType('data.yaml')).toBe('text')
      expect(getPreviewType('data.yml')).toBe('text')
    })

    it('returns text for source code files', () => {
      expect(getPreviewType('app.js')).toBe('text')
      expect(getPreviewType('app.jsx')).toBe('text')
      expect(getPreviewType('app.ts')).toBe('text')
      expect(getPreviewType('app.tsx')).toBe('text')
      expect(getPreviewType('main.py')).toBe('text')
      expect(getPreviewType('main.go')).toBe('text')
      expect(getPreviewType('main.rs')).toBe('text')
      expect(getPreviewType('main.java')).toBe('text')
      expect(getPreviewType('main.c')).toBe('text')
      expect(getPreviewType('main.cpp')).toBe('text')
      expect(getPreviewType('main.h')).toBe('text')
      expect(getPreviewType('main.hpp')).toBe('text')
      expect(getPreviewType('main.cs')).toBe('text')
      expect(getPreviewType('main.rb')).toBe('text')
      expect(getPreviewType('main.php')).toBe('text')
    })

    it('returns text for shell scripts', () => {
      expect(getPreviewType('script.sh')).toBe('text')
      expect(getPreviewType('script.bash')).toBe('text')
      expect(getPreviewType('script.zsh')).toBe('text')
    })

    it('returns text for config files with extensions', () => {
      expect(getPreviewType('config.toml')).toBe('text')
      expect(getPreviewType('config.ini')).toBe('text')
      expect(getPreviewType('config.conf')).toBe('text')
    })
  })

  describe('special filename handling', () => {
    // Note: Files without extensions return 'unsupported' in current implementation
    // because early return happens before special case check
    it('handles files without extensions as unsupported', () => {
      expect(getPreviewType('Makefile')).toBe('unsupported')
      expect(getPreviewType('Dockerfile')).toBe('unsupported')
    })

    it('handles dotfiles with valid text extensions', () => {
      // .env -> extension is 'env' which is in TEXT_EXTENSIONS
      expect(getPreviewType('.env')).toBe('text')
      // .gitignore -> extension is 'gitignore' which is in TEXT_EXTENSIONS
      expect(getPreviewType('.gitignore')).toBe('text')
    })
  })

  describe('unsupported types', () => {
    it('returns unsupported for unknown extensions', () => {
      expect(getPreviewType('archive.zip')).toBe('unsupported')
      expect(getPreviewType('archive.rar')).toBe('unsupported')
      expect(getPreviewType('doc.docx')).toBe('unsupported')
      expect(getPreviewType('spreadsheet.xlsx')).toBe('unsupported')
      expect(getPreviewType('binary.exe')).toBe('unsupported')
    })

    it('returns unsupported for files without extensions', () => {
      expect(getPreviewType('noextension')).toBe('unsupported')
    })
  })

  describe('edge cases', () => {
    it('handles multiple dots in filename', () => {
      expect(getPreviewType('file.name.with.dots.txt')).toBe('text')
      expect(getPreviewType('image.backup.png')).toBe('image')
    })

    it('handles hidden files with extensions', () => {
      expect(getPreviewType('.hidden.txt')).toBe('text')
      expect(getPreviewType('.secret.json')).toBe('text')
    })
  })
})

describe('buildPreviewUrl', () => {
  it('builds URL with WebDAV prefix', () => {
    expect(buildPreviewUrl('/documents/file.txt')).toBe(
      '/dav/documents/file.txt'
    )
  })

  it('handles root level files', () => {
    expect(buildPreviewUrl('/file.txt')).toBe('/dav/file.txt')
  })

  it('handles nested paths', () => {
    expect(buildPreviewUrl('/a/b/c/file.txt')).toBe('/dav/a/b/c/file.txt')
  })

  it('encodes special characters in segments', () => {
    expect(buildPreviewUrl('/files/my file.txt')).toBe(
      '/dav/files/my%20file.txt'
    )
  })

  it('encodes Chinese characters', () => {
    const url = buildPreviewUrl('/文档/文件.txt')
    expect(url).toContain('/dav/')
    expect(url).toContain('%E6%96%87%E6%A1%A3') // 文档 encoded
  })

  it('adds leading slash if missing', () => {
    expect(buildPreviewUrl('file.txt')).toBe('/dav/file.txt')
  })
})

describe('getLanguageFromExtension', () => {
  describe('common languages', () => {
    it('returns correct language for JavaScript', () => {
      expect(getLanguageFromExtension('file.js')).toBe('javascript')
      expect(getLanguageFromExtension('file.mjs')).toBe('javascript')
      expect(getLanguageFromExtension('file.cjs')).toBe('javascript')
    })

    it('returns jsx/tsx for React files', () => {
      expect(getLanguageFromExtension('file.jsx')).toBe('jsx')
      expect(getLanguageFromExtension('file.tsx')).toBe('tsx')
    })

    it('returns correct language for TypeScript', () => {
      expect(getLanguageFromExtension('file.ts')).toBe('typescript')
    })

    it('returns correct language for Python', () => {
      expect(getLanguageFromExtension('file.py')).toBe('python')
    })

    it('returns correct language for Go', () => {
      expect(getLanguageFromExtension('file.go')).toBe('go')
    })

    it('returns correct language for Rust', () => {
      expect(getLanguageFromExtension('file.rs')).toBe('rust')
    })

    it('returns correct language for Java', () => {
      expect(getLanguageFromExtension('file.java')).toBe('java')
    })

    it('returns correct language for C/C++', () => {
      expect(getLanguageFromExtension('file.c')).toBe('c')
      expect(getLanguageFromExtension('file.cpp')).toBe('cpp')
      expect(getLanguageFromExtension('file.h')).toBe('c')
      expect(getLanguageFromExtension('file.hpp')).toBe('cpp')
    })

    it('returns correct language for Ruby', () => {
      expect(getLanguageFromExtension('file.rb')).toBe('ruby')
    })

    it('returns correct language for PHP', () => {
      expect(getLanguageFromExtension('file.php')).toBe('php')
    })
  })

  describe('config and data formats', () => {
    it('returns correct language for JSON', () => {
      expect(getLanguageFromExtension('file.json')).toBe('json')
    })

    it('returns correct language for YAML', () => {
      expect(getLanguageFromExtension('file.yaml')).toBe('yaml')
      expect(getLanguageFromExtension('file.yml')).toBe('yaml')
    })

    it('returns correct language for XML', () => {
      expect(getLanguageFromExtension('file.xml')).toBe('xml')
    })

    it('returns correct language for TOML', () => {
      expect(getLanguageFromExtension('file.toml')).toBe('toml')
    })
  })

  describe('web technologies', () => {
    it('returns correct language for HTML', () => {
      expect(getLanguageFromExtension('file.html')).toBe('html')
      expect(getLanguageFromExtension('file.htm')).toBe('html')
    })

    it('returns correct language for CSS', () => {
      expect(getLanguageFromExtension('file.css')).toBe('css')
    })

    it('returns correct language for SCSS/SASS', () => {
      expect(getLanguageFromExtension('file.scss')).toBe('scss')
      expect(getLanguageFromExtension('file.sass')).toBe('sass')
    })

    it('returns correct language for LESS', () => {
      expect(getLanguageFromExtension('file.less')).toBe('less')
    })
  })

  describe('shell and scripts', () => {
    it('returns correct language for shell scripts', () => {
      expect(getLanguageFromExtension('file.sh')).toBe('bash')
      expect(getLanguageFromExtension('file.bash')).toBe('bash')
      expect(getLanguageFromExtension('file.zsh')).toBe('bash')
    })
  })

  describe('documentation', () => {
    it('returns correct language for Markdown', () => {
      expect(getLanguageFromExtension('file.md')).toBe('markdown')
      expect(getLanguageFromExtension('file.markdown')).toBe('markdown')
    })
  })

  describe('unknown extensions', () => {
    it('returns text for unknown extensions', () => {
      expect(getLanguageFromExtension('file.xyz')).toBe('text')
      expect(getLanguageFromExtension('file.unknown')).toBe('text')
      expect(getLanguageFromExtension('noext')).toBe('text')
    })
  })
})

describe('getMimeType', () => {
  describe('images', () => {
    it('returns correct mime type for common images', () => {
      expect(getMimeType('photo.jpg')).toBe('image/jpeg')
      expect(getMimeType('photo.jpeg')).toBe('image/jpeg')
      expect(getMimeType('image.png')).toBe('image/png')
      expect(getMimeType('animation.gif')).toBe('image/gif')
      expect(getMimeType('modern.webp')).toBe('image/webp')
    })
  })

  describe('videos', () => {
    it('returns correct mime type for common videos', () => {
      expect(getMimeType('movie.mp4')).toBe('video/mp4')
      expect(getMimeType('movie.webm')).toBe('video/webm')
      expect(getMimeType('movie.ogg')).toBe('video/ogg')
    })
  })

  describe('audio', () => {
    it('returns correct mime type for common audio', () => {
      expect(getMimeType('song.mp3')).toBe('audio/mpeg')
      expect(getMimeType('song.wav')).toBe('audio/wav')
    })
  })

  describe('documents', () => {
    it('returns correct mime type for PDF', () => {
      expect(getMimeType('doc.pdf')).toBe('application/pdf')
    })
  })

  describe('text files', () => {
    it('returns text/plain for txt files', () => {
      expect(getMimeType('readme.txt')).toBe('text/plain')
    })

    it('returns text/javascript for JS files', () => {
      expect(getMimeType('code.js')).toBe('text/javascript')
    })

    it('returns application/json for JSON files', () => {
      expect(getMimeType('config.json')).toBe('application/json')
    })
  })

  describe('unknown types', () => {
    it('returns application/octet-stream for unknown types', () => {
      expect(getMimeType('archive.zip')).toBe('application/octet-stream')
      expect(getMimeType('binary.exe')).toBe('application/octet-stream')
    })
  })
})

describe('canPreview', () => {
  it('returns true for previewable types', () => {
    expect(canPreview('image.png')).toBe(true)
    expect(canPreview('video.mp4')).toBe(true)
    expect(canPreview('audio.mp3')).toBe(true)
    expect(canPreview('doc.pdf')).toBe(true)
    expect(canPreview('code.ts')).toBe(true)
    expect(canPreview('readme.md')).toBe(true)
  })

  it('returns false for non-previewable types', () => {
    expect(canPreview('archive.zip')).toBe(false)
    expect(canPreview('doc.docx')).toBe(false)
    expect(canPreview('binary.exe')).toBe(false)
    expect(canPreview('noextension')).toBe(false)
  })
})
