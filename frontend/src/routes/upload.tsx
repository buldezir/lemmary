import { Link, Outlet } from '@tanstack/react-router'

const tabClassName =
  'rounded-md px-3 py-1.5 text-sm font-medium text-stone-600 transition-colors hover:bg-stone-200/70 hover:text-stone-950'
const tabActiveClassName = 'bg-gray-900 text-white hover:bg-gray-900 hover:text-white'

export function UploadPage() {
  return (
    <div className="mx-auto flex max-w-xl flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold text-stone-950">Upload</h1>
        <p className="mt-1 text-sm text-stone-500">
          Add documents to your library. Pick a source below.
        </p>
      </div>

      <nav aria-label="Upload sources" className="flex flex-wrap items-center gap-1">
        <Link
          to="/upload"
          activeOptions={{ exact: true }}
          className={tabClassName}
          activeProps={{ className: `${tabClassName} ${tabActiveClassName}` }}
        >
          Files
        </Link>
        <Link
          to="/upload/amazon"
          className={tabClassName}
          activeProps={{ className: `${tabClassName} ${tabActiveClassName}` }}
        >
          Amazon orders
        </Link>
        <Link
          to="/upload/split"
          className={tabClassName}
          activeProps={{ className: `${tabClassName} ${tabActiveClassName}` }}
        >
          Split documents
        </Link>
      </nav>

      <Outlet />
    </div>
  )
}
