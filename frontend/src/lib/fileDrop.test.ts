import { describe, expect, it } from 'vitest'
import { filesFromEntries, folderPrefixedName, withFolderName, type DropEntry } from './fileDrop'

function fileEntry(fullPath: string, body = 'x'): DropEntry {
  const name = fullPath.split('/').pop() ?? fullPath
  return {
    isFile: true,
    isDirectory: false,
    fullPath,
    file: (resolve) => resolve(new File([body], name, { type: 'application/pdf' })),
  }
}

/** A directory whose reader hands back `batchSize` entries per call, as Chrome's does. */
function dirEntry(fullPath: string, children: DropEntry[], batchSize = 100): DropEntry {
  return {
    isFile: false,
    isDirectory: true,
    fullPath,
    createReader: () => {
      let offset = 0
      return {
        readEntries: (resolve) => {
          const batch = children.slice(offset, offset + batchSize)
          offset += batch.length
          resolve(batch)
        },
      }
    },
  }
}

describe('folderPrefixedName', () => {
  it('keeps a file dropped on its own', () => {
    expect(folderPrefixedName('/invoice.pdf', 'invoice.pdf')).toBe('invoice.pdf')
  })

  it('prefixes the folder a file sits in', () => {
    expect(folderPrefixedName('/scans/2024/1.pdf', '1.pdf')).toBe('2024-1.pdf')
  })

  it('reads a webkitRelativePath the same way', () => {
    expect(folderPrefixedName('scans/2025/1.pdf', '1.pdf')).toBe('2025-1.pdf')
  })

  it('leaves a name that already is the folder alone', () => {
    expect(folderPrefixedName('/notes/notes', 'notes')).toBe('notes')
  })
})

describe('withFolderName', () => {
  it('returns the same file object when there is nothing to prefix', () => {
    const file = new File(['x'], 'invoice.pdf')
    expect(withFolderName(file, '/invoice.pdf')).toBe(file)
  })

  it('carries the bytes and type across the rename', async () => {
    const file = new File(['hello'], '1.pdf', { type: 'application/pdf' })
    const renamed = withFolderName(file, '/scans/2024/1.pdf')
    expect(renamed.name).toBe('2024-1.pdf')
    expect(renamed.type).toBe('application/pdf')
    expect(await renamed.text()).toBe('hello')
  })
})

describe('filesFromEntries', () => {
  it('walks nested folders to the bottom', async () => {
    const tree = [
      fileEntry('/loose.pdf'),
      dirEntry('/scans', [
        fileEntry('/scans/a.pdf'),
        dirEntry('/scans/2024', [fileEntry('/scans/2024/1.pdf')]),
      ]),
    ]
    const names = (await filesFromEntries(tree)).map((f) => f.name)
    expect(names.sort()).toEqual(['2024-1.pdf', 'loose.pdf', 'scans-a.pdf'])
  })

  it('keeps reading a directory past the first batch', async () => {
    // readEntries returns a capped batch and an empty one to finish. Calling it
    // once would silently drop everything after the cap -- this is the trap.
    const children = Array.from({ length: 250 }, (_, i) => fileEntry(`/big/${i}.pdf`))
    const files = await filesFromEntries([dirEntry('/big', children, 100)])
    expect(files).toHaveLength(250)
  })

  it('skips a file it cannot read rather than losing the drop', async () => {
    const broken: DropEntry = {
      isFile: true,
      isDirectory: false,
      fullPath: '/broken.pdf',
      file: (_resolve, reject) => reject?.(new Error('gone')),
    }
    const files = await filesFromEntries([broken, fileEntry('/good.pdf')])
    expect(files.map((f) => f.name)).toEqual(['good.pdf'])
  })
})
