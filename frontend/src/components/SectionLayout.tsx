import { Link, Outlet, type LinkProps } from '@tanstack/react-router'

export type SectionLink = {
  to: LinkProps['to']
  label: string
  /** Active only on an exact path match: for a section that sits on the page's own path. */
  exact?: boolean
}

const sectionLinkClassName =
  'border-l-2 border-transparent py-1 pl-2 pr-3 text-sm font-medium text-ink-muted transition-colors hover:border-line hover:bg-surface focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood data-[status=active]:border-oxblood data-[status=active]:bg-wash data-[status=active]:text-oxblood'

/** A full-width page shell: a header, a rail of sections, and whichever section is on the URL. */
export function SectionLayout({
  title,
  intro,
  label,
  links,
}: {
  title: string
  intro: string
  label: string
  links: SectionLink[]
}) {
  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">{title}</h1>
        <p className="mt-1 text-sm text-ink-soft">{intro}</p>
      </div>

      <div className="flex flex-col gap-5 lg:flex-row lg:items-start">
        <nav
          aria-label={label}
          className="flex flex-wrap gap-1 lg:sticky lg:top-6 lg:w-44 lg:shrink-0 lg:flex-col"
        >
          {links.map((link) => (
            <Link
              key={link.label}
              to={link.to}
              activeOptions={link.exact ? { exact: true } : undefined}
              className={sectionLinkClassName}
            >
              {link.label}
            </Link>
          ))}
        </nav>

        <div className="min-w-0 flex-1">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
