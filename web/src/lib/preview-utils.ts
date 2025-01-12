/**
 * Preview utility functions for file type detection and URL building
 */

export type PreviewType = 
  | 'text' 
  | 'image' 
  | 'pdf' 
  | 'markdown' 
  | 'video' 
  | 'audio' 
  | 'unsupported'

// Text file extensions
const TEXT_EXTENSIONS = new Set([
  'txt', 'md', 'markdown',
  'json', 'yaml', 'yml', 'toml', 'xml', 'csv',
  'js', 'jsx', 'ts', 'tsx', 'mjs', 'cjs',
  'py', 'rb', 'go', 'rs', 'java', 'kt', 'scala',
  'c', 'cpp', 'cc', 'h', 'hpp', 'cs',
  'php', 'swift', 'lua', 'pl', 'sh', 'bash', 'zsh',
  'html', 'htm', 'css', 'scss', 'sass', 'less',
  'sql', 'graphql', 'gql',
  'dockerfile', 'makefile', 'gitignore', 'gitattributes',
  'env', 'conf', 'config', 'ini', 'properties',
  'log', 'diff', 'patch',
])

// Image extensions
const IMAGE_EXTENSIONS = new Set([
  'jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'avif',
  'bmp', 'ico', 'tiff', 'tif',
])

// Video extensions
const VIDEO_EXTENSIONS = new Set([
  'mp4', 'webm', 'ogg', 'ogv', 'mov', 'avi', 'mkv', 'm4v',
])

// Audio extensions
const AUDIO_EXTENSIONS = new Set([
  'mp3', 'wav', 'ogg', 'oga', 'flac', 'aac', 'm4a', 'wma',
])

// PDF extension
const PDF_EXTENSIONS = new Set(['pdf'])

// Markdown extensions (for special rendering)
const MARKDOWN_EXTENSIONS = new Set(['md', 'markdown', 'mdown', 'mkdn'])

/**
 * Get the file extension from a filename
 */
export function getFileExtension(filename: string): string {
  const lastDot = filename.lastIndexOf('.')
  if (lastDot === -1 || lastDot === filename.length - 1) return ''
  return filename.slice(lastDot + 1).toLowerCase()
}

/**
 * Detect the preview type for a file
 */
export function getPreviewType(filename: string): PreviewType {
  const ext = getFileExtension(filename)
  if (!ext) return 'unsupported'
  
  // Check special cases for files without extensions
  const baseName = filename.toLowerCase()
  if (baseName === 'dockerfile' || baseName === 'makefile' || 
      baseName === '.gitignore' || baseName === '.gitattributes' ||
      baseName === '.env') {
    return 'text'
  }
  
  if (MARKDOWN_EXTENSIONS.has(ext)) return 'markdown'
  if (PDF_EXTENSIONS.has(ext)) return 'pdf'
  if (IMAGE_EXTENSIONS.has(ext)) return 'image'
  if (VIDEO_EXTENSIONS.has(ext)) return 'video'
  if (AUDIO_EXTENSIONS.has(ext)) return 'audio'
  if (TEXT_EXTENSIONS.has(ext)) return 'text'
  
  return 'unsupported'
}

/**
 * Check if a file can be previewed
 */
export function canPreview(filename: string): boolean {
  const type = getPreviewType(filename)
  return type !== 'unsupported'
}

/**
 * Check if a file is previewable as text
 */
export function isTextFile(filename: string): boolean {
  const type = getPreviewType(filename)
  return type === 'text' || type === 'markdown'
}

/**
 * Check if a file is previewable as image
 */
export function isImageFile(filename: string): boolean {
  return getPreviewType(filename) === 'image'
}

/**
 * Check if a file is previewable as PDF
 */
export function isPdfFile(filename: string): boolean {
  return getPreviewType(filename) === 'pdf'
}

/**
 * Check if a file is previewable as video
 */
export function isVideoFile(filename: string): boolean {
  return getPreviewType(filename) === 'video'
}

/**
 * Check if a file is previewable as audio
 */
export function isAudioFile(filename: string): boolean {
  return getPreviewType(filename) === 'audio'
}

/**
 * Build preview URL for a file
 * Uses REST API endpoint for authenticated access (avoids Basic Auth popup)
 */
export function buildPreviewUrl(path: string): string {
  // Normalize path
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  
  // Encode path segments
  const encodedPath = normalizedPath
    .split('/')
    .map(segment => encodeURIComponent(segment))
    .join('/')
  
  return `/api/v1/download${encodedPath}`
}

/**
 * Get syntax highlighter language from file extension
 */
export function getLanguageFromExtension(filename: string): string {
  const ext = getFileExtension(filename)
  
  const langMap: Record<string, string> = {
    // JavaScript/TypeScript
    js: 'javascript',
    jsx: 'jsx',
    ts: 'typescript',
    tsx: 'tsx',
    mjs: 'javascript',
    cjs: 'javascript',
    
    // Python
    py: 'python',
    
    // Go
    go: 'go',
    
    // Rust
    rs: 'rust',
    
    // Ruby
    rb: 'ruby',
    
    // Java/Kotlin
    java: 'java',
    kt: 'kotlin',
    scala: 'scala',
    
    // C/C++
    c: 'c',
    cpp: 'cpp',
    cc: 'cpp',
    h: 'c',
    hpp: 'cpp',
    cs: 'csharp',
    
    // Web
    html: 'html',
    htm: 'html',
    css: 'css',
    scss: 'scss',
    sass: 'sass',
    less: 'less',
    
    // Data formats
    json: 'json',
    yaml: 'yaml',
    yml: 'yaml',
    toml: 'toml',
    xml: 'xml',
    csv: 'csv',
    
    // Shell
    sh: 'bash',
    bash: 'bash',
    zsh: 'bash',
    
    // SQL
    sql: 'sql',
    
    // Markdown
    md: 'markdown',
    markdown: 'markdown',
    
    // PHP
    php: 'php',
    
    // Swift
    swift: 'swift',
    
    // Lua
    lua: 'lua',
    
    // Other
    dockerfile: 'dockerfile',
    makefile: 'makefile',
    graphql: 'graphql',
    gql: 'graphql',
  }
  
  return langMap[ext] || 'text'
}

/**
 * Get MIME type from file extension
 */
export function getMimeType(filename: string): string {
  const ext = getFileExtension(filename)
  
  const mimeMap: Record<string, string> = {
    // Images
    jpg: 'image/jpeg',
    jpeg: 'image/jpeg',
    png: 'image/png',
    gif: 'image/gif',
    webp: 'image/webp',
    svg: 'image/svg+xml',
    avif: 'image/avif',
    bmp: 'image/bmp',
    ico: 'image/x-icon',
    
    // Video
    mp4: 'video/mp4',
    webm: 'video/webm',
    ogg: 'video/ogg',
    ogv: 'video/ogg',
    mov: 'video/quicktime',
    
    // Audio
    mp3: 'audio/mpeg',
    wav: 'audio/wav',
    oga: 'audio/ogg',
    flac: 'audio/flac',
    aac: 'audio/aac',
    m4a: 'audio/mp4',
    
    // Documents
    pdf: 'application/pdf',
    
    // Text
    txt: 'text/plain',
    html: 'text/html',
    htm: 'text/html',
    css: 'text/css',
    js: 'text/javascript',
    json: 'application/json',
    xml: 'application/xml',
  }
  
  return mimeMap[ext] || 'application/octet-stream'
}
