import { describe, expect, it } from 'vitest'
import {
  appendNew,
  classifySelection,
  filesFromEntries,
  folderPrefixedName,
  isJunkPath,
  withFolderName,
  type DropEntry,
} from './fileDrop'

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

describe('isJunkPath', () => {
  it('drops what an archiver or Finder left behind', () => {
    expect(isJunkPath('__MACOSX/scans/._1.pdf')).toBe(true)
    expect(isJunkPath('/scans/__MACOSX/1.pdf')).toBe(true)
    expect(isJunkPath('/scans/._1.pdf')).toBe(true)
    expect(isJunkPath('/scans/.DS_Store')).toBe(true)
  })

  it('keeps real files, including ones with dots in the name', () => {
    expect(isJunkPath('/scans/1.pdf')).toBe(false)
    expect(isJunkPath('/scans/invoice.2024.pdf')).toBe(false)
    expect(isJunkPath('report.pdf')).toBe(false)
  })
})

describe('classifySelection', () => {
  const accepts = (file: File) => file.name.toLowerCase().endsWith('.pdf')
  const named = (name: string) => new File(['x'], name)

  it('drops junk silently rather than calling it the wrong type', () => {
    // An AppleDouble carries the extension of the file it shadows, so without
    // the junk check ._1.pdf is queued as a document -- and .DS_Store is
    // reported as a mistake the user did not make.
    const result = classifySelection(
      [named('1.pdf'), named('._1.pdf'), named('.DS_Store')],
      accepts,
      (file) => `scans/${file.name}`,
    )
    expect(result.accepted.map((f) => f.name)).toEqual(['1.pdf'])
    expect(result.rejected).toEqual([])
  })

  it('separates the wrong type from the wrong page', () => {
    const result = classifySelection([named('notes.odt'), named('scans.zip')], accepts)
    expect(result.rejected).toEqual(['notes.odt'])
    expect(result.zipped).toBe(true)
  })
})

describe('appendNew', () => {
  const staged = (name: string) => new File(['abc'], name, { lastModified: 1 })

  it('adds what is new and keeps what was there', () => {
    const current = [staged('a.pdf')]
    expect(appendNew(current, [staged('b.pdf')]).map((f) => f.name)).toEqual(['a.pdf', 'b.pdf'])
  })

  it('drops a file already staged, and within one selection', () => {
    const current = [staged('a.pdf')]
    expect(appendNew(current, [staged('a.pdf'), staged('b.pdf'), staged('b.pdf')])).toHaveLength(2)
  })

  it('dedupes against the list it is given, not a captured one', () => {
    // Two folder drops of the same tree can land before either has re-rendered.
    // Applied one after the other the way React applies updaters, the second
    // must see the first's result.
    const tree = [staged('1.pdf'), staged('2.pdf')]
    const afterFirst = appendNew([], tree)
    const afterSecond = appendNew(afterFirst, tree)
    expect(afterSecond).toHaveLength(2)
    expect(afterSecond).toBe(afterFirst)
  })
})
