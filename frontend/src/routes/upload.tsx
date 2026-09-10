import { useEffect, useState } from 'react'
import { Link, Outlet } from '@tanstack/react-router'

import { LimitsUsage } from '../components/LimitsUsage'
import { getLimits, type InstanceLimits } from '../lib/api/limits'
import { tabClassName } from '../components/ui'

export function UploadPage() {
  // Loaded on the shell rather than per tab, so the same figures show above
  // Files, Amazon orders and Split documents without three fetches. A failure is
  // swallowed: the allowance is context for an upload, not a precondition, and
  // the server refuses an over-limit upload whatever this shows.
  const [limits, setLimits] = useState<InstanceLimits | null>(null)

  useEffect(() => {
    let active = true
    getLimits()
      .then((next) => {
        if (active) setLimits(next)
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [])

  return (
    <div className="mx-auto flex max-w-xl flex-col gap-5">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">Upload</h1>
        <p className="mt-1 text-sm text-ink-soft">
          Add documents to your library. Pick a source below.
        </p>
      </div>

      <LimitsUsage limits={limits} />

      <nav aria-label="Upload sources" className="flex flex-wrap items-center gap-5 border-b border-line">
        <Link
          to="/upload"
          activeOptions={{ exact: true }}
          className={tabClassName}
        >
          Files
        </Link>
        <Link
          to="/upload/amazon"
          className={tabClassName}
        >
          Amazon orders
        </Link>
        <Link
          to="/upload/split"
          className={tabClassName}
        >
          Split documents
        </Link>
      </nav>

      <Outlet />
    </div>
  )
}
