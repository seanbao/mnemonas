#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

node <<'NODE'
const fs = require('fs')
const path = require('path')

const repoRoot = process.cwd()

const docs = [
  {
    file: process.env.WEBDAV_COMPATIBILITY_DOC || 'docs/webdav-compatibility.md',
    label: 'Chinese WebDAV compatibility document',
    matrixStart: '## 兼容性矩阵',
    matrixEnd: '## 真实客户端验证标准',
    allowedStatuses: new Set(['已验证', '预期可用', '需要配置', '需要验证']),
    requiredRows: [
      'Nautilus / GNOME Files',
      'Dolphin',
      'davfs2',
      'rclone',
      'Finder',
      'Transmit',
      'Cyberduck',
      'File Explorer',
      'WinSCP',
      'NetDrive',
      'Files',
      'Documents by Readdle',
      'FileBrowser',
      'Solid Explorer',
      'Total Commander + WebDAV plugin',
      'FolderSync',
      'Infuse',
      'nPlayer',
      'VLC',
      'Kodi',
    ],
    requiredText: [
      '## 真实客户端验证标准',
      '先运行 curl 协议 smoke',
      '`scripts/webdav-client-smoke.sh`',
      '`RUN_RCLONE_WEBDAV=1`',
      '连接或挂载',
      '浏览目录',
      '上传、下载、重命名、删除',
      '大文件传输',
      '媒体拖动',
      '离线同步',
      '客户端名称和版本',
      '操作系统和版本',
      'WebDAV 兼容性报告表单',
    ],
  },
  {
    file: process.env.WEBDAV_COMPATIBILITY_DOC_EN || 'docs/webdav-compatibility.en.md',
    label: 'English WebDAV compatibility document',
    matrixStart: '## Compatibility Matrix',
    matrixEnd: '## Real-Client Validation Standard',
    allowedStatuses: new Set(['Verified', 'Expected', 'Needs configuration', 'Needs validation']),
    requiredRows: [
      'Nautilus / GNOME Files',
      'Dolphin',
      'davfs2',
      'rclone',
      'Finder',
      'Transmit',
      'Cyberduck',
      'File Explorer',
      'WinSCP',
      'NetDrive',
      'Files',
      'Documents by Readdle',
      'FileBrowser',
      'Solid Explorer',
      'Total Commander + WebDAV plugin',
      'FolderSync',
      'Infuse',
      'nPlayer',
      'VLC',
      'Kodi',
    ],
    requiredText: [
      '## Real-Client Validation Standard',
      'Run the curl protocol smoke first',
      '`scripts/webdav-client-smoke.sh`',
      '`RUN_RCLONE_WEBDAV=1`',
      'connect or mount',
      'browse directories',
      'upload, download, rename, and delete',
      'large-file transfer',
      'media seeking',
      'offline sync',
      'Client name and version',
      'Operating system and version',
      'WebDAV compatibility report form',
    ],
  },
]

const readmeClientSummaries = [
  {
    file: process.env.WEBDAV_README || 'README.md',
    topRequiredText: [
      'WebDAV 协议入口覆盖主要访问路径',
      '客户端兼容状态按矩阵持续跟踪',
    ],
    topForbiddenText: [
      '常见 WebDAV 客户端均可访问',
    ],
    sectionStart: '### 客户端连接',
    sectionEnd: '## 项目结构',
    requiredText: [
      '[WebDAV 兼容性](docs/webdav-compatibility.md)',
      '兼容状态',
      '真实客户端 E2E 覆盖',
      '仍按矩阵继续跟踪',
      '| 平台 | 常见客户端 | 连接地址 |',
    ],
    forbiddenText: [
      '| 平台 | 推荐客户端 | 连接地址 |',
    ],
  },
  {
    file: process.env.WEBDAV_README_EN || 'README.en.md',
    topRequiredText: [
      'WebDAV protocol access cover the main access paths',
      'client compatibility tracked in the matrix',
    ],
    topForbiddenText: [
      'common WebDAV clients are both supported',
    ],
    sectionStart: '### WebDAV Clients',
    sectionEnd: '## Repository Layout',
    requiredText: [
      '[WebDAV Compatibility](docs/webdav-compatibility.en.md)',
      'compatibility status',
      'real-client E2E coverage',
      'remain tracked by the matrix',
      '| Platform | Common Client | URL |',
    ],
    forbiddenText: [
      '| Platform | Recommended Client | URL |',
    ],
  },
]

const mountingGuides = [
  {
    file: process.env.WEBDAV_MOUNTING_GUIDE || 'docs/mounting-guide.md',
    sectionEnd: '## 连接信息',
    requiredText: [
      '[WebDAV 客户端兼容性](webdav-compatibility.md)',
      '实际兼容状态',
      '真实客户端 E2E 覆盖',
      '仍按矩阵跟踪',
    ],
  },
  {
    file: process.env.WEBDAV_MOUNTING_GUIDE_EN || 'docs/mounting-guide.en.md',
    sectionEnd: '## Connection Information',
    requiredText: [
      '[WebDAV compatibility](webdav-compatibility.en.md)',
      'actual compatibility status',
      'real-client E2E coverage',
      'remain tracked by the matrix',
    ],
  },
]

const errors = []

function readDoc(file) {
  const absolute = path.isAbsolute(file) ? path.resolve(file) : path.resolve(repoRoot, file)
  try {
    return fs.readFileSync(absolute, 'utf8')
  } catch (error) {
    errors.push(`${file}: ${error.message}`)
    return null
  }
}

function extractSection(markdown, startMarker, endMarker, file) {
  const start = markdown.indexOf(startMarker)
  if (start === -1) {
    errors.push(`${file}: missing section ${startMarker}`)
    return ''
  }
  const end = markdown.indexOf(endMarker, start + startMarker.length)
  if (end === -1) {
    errors.push(`${file}: missing section ${endMarker}`)
    return markdown.slice(start)
  }
  return markdown.slice(start, end)
}

function tableRows(markdown) {
  const rows = []
  for (const line of markdown.split('\n')) {
    if (!/^\|.*\|$/.test(line.trim())) {
      continue
    }
    if (/^\|\s*:?-{3,}:?\s*(?:\|\s*:?-{3,}:?\s*)+\|$/.test(line.trim())) {
      continue
    }
    const cells = line
      .trim()
      .slice(1, -1)
      .split('|')
      .map((cell) => cell.trim().replace(/\*\*/g, ''))
    rows.push(cells)
  }
  return rows
}

function checkRequiredText(markdown, doc) {
  for (const text of doc.requiredText) {
    if (!markdown.includes(text)) {
      errors.push(`${doc.file}: missing required WebDAV compatibility text: ${text}`)
    }
  }
}

function checkReadmeClientSummary(readme) {
  const markdown = readDoc(readme.file)
  if (markdown === null) {
    return
  }

  for (const text of readme.topRequiredText ?? []) {
    if (!markdown.includes(text)) {
      errors.push(`${readme.file}: missing required README WebDAV overview text: ${text}`)
    }
  }
  for (const text of readme.topForbiddenText ?? []) {
    if (markdown.includes(text)) {
      errors.push(`${readme.file}: avoid overclaiming WebDAV client support in README: ${text}`)
    }
  }
  const section = extractSection(markdown, readme.sectionStart, readme.sectionEnd, readme.file)
  for (const text of readme.requiredText) {
    if (!section.includes(text)) {
      errors.push(`${readme.file}: missing required README WebDAV client-summary text: ${text}`)
    }
  }
  for (const text of readme.forbiddenText) {
    if (section.includes(text)) {
      errors.push(`${readme.file}: avoid overclaiming WebDAV client support in README: ${text}`)
    }
  }
}

function checkMountingGuideCompatibilityNote(guide) {
  const markdown = readDoc(guide.file)
  if (markdown === null) {
    return
  }

  const sectionEnd = markdown.indexOf(guide.sectionEnd)
  if (sectionEnd === -1) {
    errors.push(`${guide.file}: missing section ${guide.sectionEnd}`)
    return
  }
  const intro = markdown.slice(0, sectionEnd)
  for (const text of guide.requiredText) {
    if (!intro.includes(text)) {
      errors.push(`${guide.file}: missing required WebDAV mounting-guide compatibility note: ${text}`)
    }
  }
}

function checkMatrixRows(matrixMarkdown, doc) {
  const rows = tableRows(matrixMarkdown)
  const clientRows = rows.filter((cells) => cells.length === 4 && cells[0] !== '客户端' && cells[0] !== 'Client')
  const clients = new Set(clientRows.map((cells) => cells[0]))

  for (const client of doc.requiredRows) {
    if (!clients.has(client)) {
      errors.push(`${doc.file}: missing required WebDAV compatibility matrix row: ${client}`)
    }
  }

  for (const cells of clientRows) {
    const status = cells[2]
    if (!doc.allowedStatuses.has(status)) {
      errors.push(`${doc.file}: unsupported WebDAV compatibility status for ${cells[0]}: ${status}`)
    }
  }
}

for (const doc of docs) {
  const markdown = readDoc(doc.file)
  if (markdown === null) {
    continue
  }
  checkRequiredText(markdown, doc)
  const matrixMarkdown = extractSection(markdown, doc.matrixStart, doc.matrixEnd, doc.file)
  checkMatrixRows(matrixMarkdown, doc)
}
for (const readme of readmeClientSummaries) {
  checkReadmeClientSummary(readme)
}
for (const guide of mountingGuides) {
  checkMountingGuideCompatibilityNote(guide)
}

if (errors.length > 0) {
  for (const error of errors) {
    console.error(`[webdav-compat-docs] ${error}`)
  }
  process.exit(1)
}

console.log('[webdav-compat-docs] checked WebDAV compatibility matrix, validation standard, README client summary, and mounting guide note')
NODE
