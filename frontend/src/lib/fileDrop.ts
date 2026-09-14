/**
 * Expanding a dropped or picked folder into a flat File[]. The browser offers
 * two unrelated ways to hand one over, `webkitdirectory` on an input and the
 * drag-and-drop entry API, and this is where both become the same thing.
 */

/**
 * Written out rather than taken from lib.dom so the recursion can be tested
 * with plain objects: real entries only exist inside a drop event.
 */
export type DropEntry = {
  isFile: boolean
  isDirectory: boolean
  /** Path within the drop, e.g. "/invoices/2024/1.pdf". */
  fullPath: string
  file?: (resolve: (file: File) => void, reject?: (err: unknown) => void) => void
  createReader?: () => {
    readEntries: (resolve: (entries: DropEntry[]) => void, reject?: (err: unknown) => void) => void
  }
}

/** The lowercased extension including the dot, or "" when there is none. */
export function extensionOf(name: string): string {
  const base = name.slice(name.lastIndexOf('/') + 1)
  const dot = base.lastIndexOf('.')
  return dot > 0 ? base.slice(dot).toLowerCase() : ''
}

/**
 * Filesystem bookkeeping nobody means to upload: macOS resource forks and any
 * dot-file. Mirrors isJunkEntry in backend/internal/zipimport, so a folder and
 * a zip of the same tree agree.
 *
 * Checked on the path rather than the name, because by the time a file is
 * renamed for its folder `._1.pdf` reads as `scans-._1.pdf`.
 */
export function isJunkPath(path: string): boolean {
  const normalized = path.replace(/\\/g, '/')
  if (normalized.startsWith('__MACOSX/') || normalized.includes('/__MACOSX/')) return true
  const base = normalized.slice(normalized.lastIndexOf('/') + 1)
  return base.startsWith('.')
}

/**
 * Mirrors documentName in backend/internal/zipimport, so a folder and a zip of
 * the same tree produce the same names. The parent folder is kept as a prefix
 * because it is often the only thing telling two 1.pdf files apart.
 */
export function folderPrefixedName(relativePath: string, name: string): string {
  const parts = relativePath.replace(/\\/g, '/').split('/').filter(Boolean)
  const parent = parts.length >= 2 ? parts[parts.length - 2] : ''
  if (!parent || parent === name) return name
  return `${parent}-${name}`
}

/** The same file under its folder-prefixed name, or itself when it has no folder. */
export function withFolderName(file: File, relativePath: string): File {
  const name = folderPrefixedName(relativePath, file.name)
  if (name === file.name) return file
  return new File([file], name, { type: file.type, lastModified: file.lastModified })
}

function readFile(entry: DropEntry): Promise<File | null> {
  if (!entry.file) return Promise.resolve(null)
  return new Promise((resolve) => {
    entry.file?.(
      (file) => resolve(withFolderName(file, entry.fullPath)),
      // One unreadable file should not lose the rest of the drop.
      () => resolve(null),
    )
  })
}

function readDirectory(entry: DropEntry): Promise<DropEntry[]> {
  const reader = entry.createReader?.()
  if (!reader) return Promise.resolve([])
  // readEntries returns a batch, not the directory (Chrome caps it at 100),
  // and signals the end with an empty one. Calling it once silently truncates.
  return new Promise((resolve) => {
    const found: DropEntry[] = []
    const next = () =>
      reader.readEntries((batch) => {
        if (batch.length === 0) {
          resolve(found)
          return
        }
        found.push(...batch)
        next()
      }, () => resolve(found))
    next()
  })
}

/** Every file under these entries, folders walked to the bottom. */
export async function filesFromEntries(entries: DropEntry[]): Promise<File[]> {
  const files: File[] = []
  const queue = [...entries]
  while (queue.length > 0) {
    const entry = queue.shift()!
    if (isJunkPath(entry.fullPath)) continue
    if (entry.isFile) {
      const file = await readFile(entry)
      if (file) files.push(file)
    } else if (entry.isDirectory) {
      queue.push(...(await readDirectory(entry)))
    }
  }
  return files
}

/**
 * The entries are collected before the first await on purpose: the items list
 * is emptied once the drop handler returns.
 */
export async function filesFromDataTransfer(dt: DataTransfer): Promise<File[]> {
  const entries: DropEntry[] = []
  for (const item of Array.from(dt.items ?? [])) {
    const entry = item.webkitGetAsEntry?.() as DropEntry | null
    if (entry) entries.push(entry)
  }
  // No entry API (or a drop carrying no entries): the plain file list is all
  // there is, and it never holds folders.
  if (entries.length === 0) return Array.from(dt.files ?? [])
  return filesFromEntries(entries)
}

/** What one selection contributes, before it meets the list already staged. */
export type Classified = {
  accepted: File[]
  /** Names to report as the wrong type. Junk is dropped silently instead. */
  rejected: string[]
  /** A zip is not the wrong type, it is the wrong page. */
  zipped: boolean
}

/** Depends on nothing but its arguments, so it can run before state is touched. */
export function classifySelection(
  incoming: File[],
  accepts: (file: File) => boolean,
  pathOf: (file: File) => string = (file) => file.webkitRelativePath || file.name,
): Classified {
  const accepted: File[] = []
  const rejected: string[] = []
  let zipped = false

  for (const file of incoming) {
    if (isJunkPath(pathOf(file))) continue
    if (accepts(file)) {
      accepted.push(file)
      continue
    }
    if (extensionOf(file.name) === '.zip') zipped = true
    else rejected.push(file.name)
  }
  return { accepted, rejected, zipped }
}

/** How two files are told apart when the same one is offered twice. */
export function fileKey(file: File): string {
  return `${file.name}:${file.size}:${file.lastModified}`
}

/**
 * Deduplicates against the list it is given rather than a captured one: walking
 * a folder is asynchronous, so two drops can land before either re-renders and
 * a stale snapshot would stage the same tree twice.
 */
export function appendNew(current: File[], accepted: File[]): File[] {
  const seen = new Set(current.map(fileKey))
  const added: File[] = []
  for (const file of accepted) {
    const key = fileKey(file)
    if (seen.has(key)) continue
    seen.add(key)
    added.push(file)
  }
  return added.length === 0 ? current : [...current, ...added]
}
