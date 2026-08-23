import { crc32 } from 'node:zlib'

export type ZipEntry = {
  name: string
  body: string | Buffer
}

/**
 * Builds a zip archive in memory using stored (uncompressed) entries, so tests
 * can create an Amazon-shaped export without a fixture file or a zip library.
 */
export function buildZip(entries: ZipEntry[]): Buffer {
  const locals: Buffer[] = []
  const centrals: Buffer[] = []
  let offset = 0

  for (const entry of entries) {
    const name = Buffer.from(entry.name, 'utf8')
    const body = Buffer.isBuffer(entry.body) ? entry.body : Buffer.from(entry.body, 'utf8')
    const checksum = crc32(body)

    const localHeader = Buffer.alloc(30)
    localHeader.writeUInt32LE(0x04034b50, 0) // local file header signature
    localHeader.writeUInt16LE(20, 4) // version needed
    localHeader.writeUInt16LE(0x0800, 6) // UTF-8 names
    localHeader.writeUInt16LE(0, 8) // stored, no compression
    localHeader.writeUInt32LE(checksum, 14)
    localHeader.writeUInt32LE(body.length, 18)
    localHeader.writeUInt32LE(body.length, 22)
    localHeader.writeUInt16LE(name.length, 26)
    locals.push(localHeader, name, body)

    const centralHeader = Buffer.alloc(46)
    centralHeader.writeUInt32LE(0x02014b50, 0) // central directory signature
    centralHeader.writeUInt16LE(20, 4) // version made by
    centralHeader.writeUInt16LE(20, 6) // version needed
    centralHeader.writeUInt16LE(0x0800, 8) // UTF-8 names
    centralHeader.writeUInt16LE(0, 10) // stored, no compression
    centralHeader.writeUInt32LE(checksum, 16)
    centralHeader.writeUInt32LE(body.length, 20)
    centralHeader.writeUInt32LE(body.length, 24)
    centralHeader.writeUInt16LE(name.length, 28)
    centralHeader.writeUInt32LE(offset, 42)
    centrals.push(centralHeader, name)

    offset += localHeader.length + name.length + body.length
  }

  const centralDirectory = Buffer.concat(centrals)
  const end = Buffer.alloc(22)
  end.writeUInt32LE(0x06054b50, 0) // end of central directory signature
  end.writeUInt16LE(entries.length, 8)
  end.writeUInt16LE(entries.length, 10)
  end.writeUInt32LE(centralDirectory.length, 12)
  end.writeUInt32LE(offset, 16)

  return Buffer.concat([...locals, centralDirectory, end])
}

/** Builds an archive shaped like Amazon's "Your Orders" export. */
export function amazonExportZip(invoices: string[]): Buffer {
  return buildZip([
    { name: 'Your Orders/Your Amazon Orders/Order History.csv', body: 'order,history\n1,2\n' },
    {
      name: 'Your Orders/Your Amazon Orders/Media/YourOrders.PhotoOnDelivery/media/a1b2.jpeg',
      body: 'not-a-real-jpeg',
    },
    ...invoices.map((body, index) => ({
      name: `Your Orders/Additional Data/Retail.TransactionalInvoicing.2.1/${index + 1}.pdf`,
      body,
    })),
  ])
}

/** A unique invoice body so repeated runs are not detected as duplicates. */
export function uniqueInvoice(label: string): string {
  return `%PDF-1.4 amazon invoice ${label} ${Date.now()} ${Math.random().toString(16).slice(2)}`
}
