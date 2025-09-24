#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

node <<'NODE'
const cp = require('child_process')
const fs = require('fs')
const path = require('path')

const repoRoot = process.cwd()
const files = [
  ...cp.execFileSync('git', ['ls-files', '--', '*.md'], { encoding: 'utf8' }).split('\n'),
  ...cp.execFileSync('git', ['ls-files', '--others', '--exclude-standard', '--', '*.md'], { encoding: 'utf8' }).split('\n'),
]
  .filter(Boolean)
  .filter((file, index, items) => items.indexOf(file) === index)
const fileSet = new Set(files)

const errors = []
const anchorsByFile = new Map()
const documentationIndexFiles = new Set(['docs/README.md', 'docs/README.en.md'])
const requiredDocumentPairs = [
  ['README.md', 'README.en.md', 'English', 'Chinese'],
  ['CHANGELOG.md', 'CHANGELOG.en.md', 'English', 'Chinese'],
  ['SUPPORT.md', 'SUPPORT.en.md', 'English', 'Chinese'],
  ['SECURITY.zh-CN.md', 'SECURITY.md', 'English', 'Chinese'],
  ['web/README.md', 'web/README.en.md', 'English', 'Chinese'],
]

function checkRequiredDocumentPairs() {
  for (const [chineseFile, englishFile, englishLabel, chineseLabel] of requiredDocumentPairs) {
    if (fileSet.has(chineseFile) && !fileSet.has(englishFile)) {
      errors.push(`${chineseFile}: missing ${englishLabel} documentation pair: ${englishFile}`)
    }
    if (fileSet.has(englishFile) && !fileSet.has(chineseFile)) {
      errors.push(`${englishFile}: missing ${chineseLabel} documentation pair: ${chineseFile}`)
    }
  }
}

function checkDocumentationPairs() {
  for (const file of files) {
    const parsed = path.parse(file)
    if (parsed.dir !== 'docs' || parsed.ext !== '.md') {
      continue
    }

    if (parsed.name.endsWith('.en')) {
      const chineseFile = path.join(parsed.dir, `${parsed.name.slice(0, -3)}.md`)
      if (!fileSet.has(chineseFile)) {
        errors.push(`${file}: missing Chinese documentation pair: ${chineseFile}`)
      }
      continue
    }

    const englishFile = path.join(parsed.dir, `${parsed.name}.en.md`)
    if (!fileSet.has(englishFile)) {
      errors.push(`${file}: missing English documentation pair: ${englishFile}`)
    }
  }
}

function checkDocumentationIndexCoverage() {
  const chineseIndex = readOptionalFile('docs/README.md')
  const englishIndex = readOptionalFile('docs/README.en.md')

  for (const file of files) {
    const parsed = path.parse(file)
    if (parsed.dir !== 'docs' || parsed.ext !== '.md' || documentationIndexFiles.has(file) || parsed.name.endsWith('.en')) {
      continue
    }

    const englishFile = path.join(parsed.dir, `${parsed.name}.en.md`)
    if (chineseIndex !== null) {
      if (!containsMarkdownLinkTarget(chineseIndex, file)) {
        errors.push(`docs/README.md: missing documentation index entry: ${file}`)
      }
      if (fileSet.has(englishFile) && !containsMarkdownLinkTarget(chineseIndex, englishFile)) {
        errors.push(`docs/README.md: missing documentation index entry: ${englishFile}`)
      }
    }
    if (englishIndex !== null && fileSet.has(englishFile) && !containsMarkdownLinkTarget(englishIndex, englishFile)) {
      errors.push(`docs/README.en.md: missing documentation index entry: ${englishFile}`)
    }
  }
}

function readOptionalFile(filePath) {
  if (!fileSet.has(filePath)) {
    return null
  }
  return fs.readFileSync(path.join(repoRoot, filePath), 'utf8')
}

function containsMarkdownLinkTarget(markdown, targetFile) {
  const escapedTarget = targetFile.startsWith('docs/') ? targetFile.slice('docs/'.length) : targetFile
  return extractMarkdownLinkTargets(markdown).some((target) => {
    const normalized = normalizeTarget(target)
    return normalized?.pathTarget === escapedTarget
  })
}

function normalizeTarget(rawTarget) {
  let target = rawTarget.trim()
  if (!target) {
    return null
  }
  if (/^[a-z][a-z0-9+.-]*:/i.test(target)) {
    return null
  }
  if (target.startsWith('<') && target.endsWith('>')) {
    target = target.slice(1, -1)
  } else {
    target = target.split(/\s+/, 1)[0]
  }
  const hashIndex = target.indexOf('#')
  const fragment = hashIndex >= 0 ? target.slice(hashIndex + 1) : ''
  const pathTarget = (hashIndex >= 0 ? target.slice(0, hashIndex) : target).split('?', 1)[0]
  if (!pathTarget && !fragment) {
    return null
  }
  try {
    return {
      pathTarget: pathTarget ? decodeURIComponent(pathTarget) : '',
      fragment: fragment ? decodeURIComponent(fragment) : '',
      hasFragment: hashIndex >= 0,
    }
  } catch (error) {
    return { pathTarget, fragment, hasFragment: hashIndex >= 0 }
  }
}

function checkTarget(sourceFile, rawTarget) {
  const link = normalizeTarget(rawTarget)
  if (!link) {
    return
  }

  const sourceDir = path.dirname(path.join(repoRoot, sourceFile))
  const resolved = link.pathTarget
    ? path.normalize(path.join(sourceDir, link.pathTarget))
    : path.join(repoRoot, sourceFile)
  if (resolved !== repoRoot && !resolved.startsWith(repoRoot + path.sep)) {
    errors.push(`${sourceFile}: link escapes repository: ${rawTarget}`)
    return
  }
  if (!fs.existsSync(resolved)) {
    errors.push(`${sourceFile}: missing link target: ${rawTarget}`)
    return
  }
  if (link.hasFragment && link.fragment && resolved.endsWith('.md')) {
    const anchors = getMarkdownAnchors(resolved)
    const normalizedAnchor = normalizeAnchor(link.fragment)
    if (!anchors.has(normalizedAnchor)) {
      errors.push(`${sourceFile}: missing heading anchor: ${rawTarget}`)
    }
  }
}

function normalizeAnchor(fragment) {
  return fragment.trim().toLowerCase()
}

function getMarkdownAnchors(filePath) {
  const relativePath = path.relative(repoRoot, filePath)
  if (anchorsByFile.has(relativePath)) {
    return anchorsByFile.get(relativePath)
  }

  const anchors = new Set()
  const seen = new Map()
  let inFence = false
  const text = fs.readFileSync(filePath, 'utf8')

  for (const line of text.split('\n')) {
    if (/^\s{0,3}(```|~~~)/.test(line)) {
      inFence = !inFence
      continue
    }
    if (inFence) {
      continue
    }

    const match = /^\s{0,3}#{1,6}\s+(.+?)\s*$/.exec(line)
    if (!match) {
      continue
    }

    const heading = match[1].replace(/\s+#+\s*$/, '')
    const baseSlug = slugHeading(heading)
    if (!baseSlug) {
      continue
    }
    const count = seen.get(baseSlug) ?? 0
    seen.set(baseSlug, count + 1)
    anchors.add(count === 0 ? baseSlug : `${baseSlug}-${count}`)
  }

  anchorsByFile.set(relativePath, anchors)
  return anchors
}

function slugHeading(heading) {
  return heading
    .trim()
    .toLowerCase()
    .replace(/<[^>]*>/g, '')
    .replace(/[`*_~]/g, '')
    .replace(/[!"#$%&'()*+,./:;<=>?@[\\\]^{}|，。！？、；：（）【】《》“”‘’]/g, '')
    .replace(/\s+/g, '-')
}

checkRequiredDocumentPairs()
checkDocumentationPairs()
checkDocumentationIndexCoverage()

for (const file of files) {
  const text = fs.readFileSync(path.join(repoRoot, file), 'utf8')

  checkJsonCodeFences(file, text)
  for (const target of extractMarkdownLinkTargets(text)) {
    checkTarget(file, target)
  }
}

function checkJsonCodeFences(sourceFile, markdown) {
  const lines = markdown.split('\n')
  let inFence = false
  let fenceChar = ''
  let language = ''
  let startLine = 0
  let content = []

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    if (!inFence) {
      const match = /^ {0,3}(`{3,}|~{3,})\s*(.*)$/.exec(line)
      if (!match) {
        continue
      }
      inFence = true
      fenceChar = match[1][0]
      language = (match[2] || '').trim().split(/\s+/, 1)[0].toLowerCase()
      startLine = index + 1
      content = []
      continue
    }

    const closePattern = fenceChar === '`' ? /^ {0,3}`{3,}\s*$/ : /^ {0,3}~{3,}\s*$/
    if (closePattern.test(line)) {
      if (language === 'json') {
        try {
          JSON.parse(content.join('\n'))
        } catch (error) {
          errors.push(`${sourceFile}:${startLine}: invalid json code fence: ${error.message}`)
        }
      }
      inFence = false
      fenceChar = ''
      language = ''
      startLine = 0
      content = []
      continue
    }

    content.push(line)
  }
}

function extractMarkdownLinkTargets(markdown) {
  const targets = []
  const inlineLink = /\[[^\]\n]+\]\(([^\)\n]+)\)/g
  const referenceLink = /^\s*\[[^\]\n]+\]:\s+(\S+)/gm
  for (const pattern of [inlineLink, referenceLink]) {
    let match
    while ((match = pattern.exec(markdown)) !== null) {
      targets.push(match[1])
    }
  }
  return targets
}

if (errors.length > 0) {
  console.error('Documentation link check failed:')
  for (const error of errors) {
    console.error(`  - ${error}`)
  }
  process.exit(1)
}

console.log(`[docs-link-check] checked ${files.length} markdown files`)
NODE

toml_check_program="$(mktemp "${TMPDIR:-/tmp}/mnemonas-doc-toml-check.XXXXXX.go")"
trap 'rm -f -- "$toml_check_program"' EXIT
cat > "$toml_check_program" <<'GO'
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

func gitFiles(args ...string) ([]string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func main() {
	tracked, err := gitFiles("ls-files", "--", "*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list tracked markdown files: %v\n", err)
		os.Exit(1)
	}
	untracked, err := gitFiles("ls-files", "--others", "--exclude-standard", "--", "*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list untracked markdown files: %v\n", err)
		os.Exit(1)
	}

	seen := map[string]bool{}
	files := make([]string, 0, len(tracked)+len(untracked))
	for _, file := range append(tracked, untracked...) {
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}

	errors := []string{}
	tomlFenceCount := 0
	openFence := regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*(.*)$")
	closeBacktick := regexp.MustCompile("^ {0,3}`{3,}\\s*$")
	closeTilde := regexp.MustCompile("^ {0,3}~{3,}\\s*$")

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read file: %v", file, err))
			continue
		}

		inFence := false
		fenceChar := ""
		language := ""
		startLine := 0
		content := []string{}

		for index, line := range strings.Split(string(data), "\n") {
			lineNumber := index + 1
			if !inFence {
				match := openFence.FindStringSubmatch(line)
				if match == nil {
					continue
				}
				inFence = true
				fenceChar = match[1][:1]
				fields := strings.Fields(strings.TrimSpace(match[2]))
				if len(fields) > 0 {
					language = strings.ToLower(fields[0])
				} else {
					language = ""
				}
				startLine = lineNumber
				content = []string{}
				continue
			}

			closed := (fenceChar == "`" && closeBacktick.MatchString(line)) || (fenceChar == "~" && closeTilde.MatchString(line))
			if closed {
				if language == "toml" {
					tomlFenceCount++
					raw := strings.TrimSpace(strings.Join(content, "\n"))
					if raw != "" {
						var decoded map[string]any
						if err := toml.Unmarshal([]byte(raw), &decoded); err != nil {
							errors = append(errors, fmt.Sprintf("%s:%d: invalid toml code fence: %v", file, startLine, err))
						}
					}
				}
				inFence = false
				fenceChar = ""
				language = ""
				startLine = 0
				content = []string{}
				continue
			}

			content = append(content, line)
		}
	}

	if len(errors) > 0 {
		fmt.Fprintln(os.Stderr, "Documentation TOML example check failed:")
		for _, err := range errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", err)
		}
		os.Exit(1)
	}

	fmt.Printf("[docs-toml-check] checked %d TOML code fences\n", tomlFenceCount)
}
GO
GOTOOLCHAIN=local go run "$toml_check_program"
