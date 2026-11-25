export interface ParagraphDiffEntry {
  kind: 'equal' | 'added' | 'removed'
  text: string
  oldIndex?: number
  newIndex?: number
}

export interface DocxDiffSummary {
  entries: ParagraphDiffEntry[]
  addedCount: number
  removedCount: number
  unchangedCount: number
}

/**
 * Computes a clean paragraph-level LCS redline diff between a baseline .docx
 * and the current .docx (`oldParagraphs` -> `newParagraphs`).
 */
export function diffDocxParagraphs(oldParagraphs: string[], newParagraphs: string[]): DocxDiffSummary {
  const m = oldParagraphs.length
  const n = newParagraphs.length

  // Guard against extreme documents (fallback to bounded linear match > 1200 paragraphs)
  if (m * n > 1_440_000) {
    return fallbackLinearDiff(oldParagraphs, newParagraphs)
  }

  const dp: Uint32Array[] = Array.from({ length: m + 1 }, () => new Uint32Array(n + 1))
  for (let i = m - 1; i >= 0; i--) {
    const row = dp[i]
    const nextRow = dp[i + 1]
    for (let j = n - 1; j >= 0; j--) {
      if (oldParagraphs[i] === newParagraphs[j]) {
        row[j] = nextRow[j + 1] + 1
      } else {
        row[j] = nextRow[j] >= row[j + 1] ? nextRow[j] : row[j + 1]
      }
    }
  }

  const entries: ParagraphDiffEntry[] = []
  let i = 0
  let j = 0
  let addedCount = 0
  let removedCount = 0
  let unchangedCount = 0

  while (i < m && j < n) {
    if (oldParagraphs[i] === newParagraphs[j]) {
      entries.push({ kind: 'equal', text: newParagraphs[j], oldIndex: i, newIndex: j })
      unchangedCount++
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      entries.push({ kind: 'removed', text: oldParagraphs[i], oldIndex: i })
      removedCount++
      i++
    } else {
      entries.push({ kind: 'added', text: newParagraphs[j], newIndex: j })
      addedCount++
      j++
    }
  }
  while (i < m) {
    entries.push({ kind: 'removed', text: oldParagraphs[i], oldIndex: i })
    removedCount++
    i++
  }
  while (j < n) {
    entries.push({ kind: 'added', text: newParagraphs[j], newIndex: j })
    addedCount++
    j++
  }

  return { entries, addedCount, removedCount, unchangedCount }
}

function fallbackLinearDiff(oldParagraphs: string[], newParagraphs: string[]): DocxDiffSummary {
  const oldSet = new Set(oldParagraphs)
  const newSet = new Set(newParagraphs)
  const entries: ParagraphDiffEntry[] = []
  let addedCount = 0
  let removedCount = 0
  let unchangedCount = 0

  oldParagraphs.forEach((p, idx) => {
    if (!newSet.has(p)) {
      entries.push({ kind: 'removed', text: p, oldIndex: idx })
      removedCount++
    }
  })
  newParagraphs.forEach((p, idx) => {
    if (oldSet.has(p)) {
      entries.push({ kind: 'equal', text: p, newIndex: idx })
      unchangedCount++
    } else {
      entries.push({ kind: 'added', text: p, newIndex: idx })
      addedCount++
    }
  })
  return { entries, addedCount, removedCount, unchangedCount }
}
