const githubRepoUrl = 'https://github.com/buldezir/lemmary'

function GitHubIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="currentColor"
      className="h-3.5 w-3.5"
      aria-hidden="true"
    >
      <path d="M12 0C5.37 0 0 5.37 0 12c0 5.3 3.44 9.8 8.21 11.39.6.11.82-.26.82-.58 0-.28-.01-1.02-.02-2-3.34.73-4.04-1.61-4.04-1.61-.55-1.39-1.33-1.76-1.33-1.76-1.09-.74.08-.73.08-.73 1.2.09 1.84 1.24 1.84 1.24 1.07 1.84 2.81 1.31 3.5 1 .11-.78.42-1.31.76-1.61-2.67-.3-5.47-1.33-5.47-5.93 0-1.31.47-2.38 1.24-3.22-.12-.3-.54-1.52.12-3.18 0 0 1.01-.32 3.3 1.23a11.5 11.5 0 0 1 3-.4c1.02.01 2.05.14 3 .4 2.29-1.55 3.3-1.23 3.3-1.23.66 1.66.24 2.88.12 3.18.77.84 1.24 1.91 1.24 3.22 0 4.61-2.81 5.62-5.49 5.92.43.37.81 1.1.81 2.22 0 1.61-.01 2.91-.01 3.31 0 .32.22.69.83.57C20.56 21.8 24 17.3 24 12 24 5.37 18.63 0 12 0z" />
    </svg>
  )
}

export function AppFooter() {
  return (
    <footer className="mt-auto border-t border-line py-5 text-center text-[11px] uppercase tracking-[0.14em] text-ink-soft">
      <p className="inline-flex items-center">
        Lemmary
        <span className="mx-2 text-line-strong" aria-hidden="true">
          ·
        </span>
        <a
          href="/docs"
          target="_blank"
          rel="noopener noreferrer"
          aria-label="Documentation"
          title="Documentation"
          className="inline-flex transition-colors hover:text-oxblood"
        >
          Documentation
        </a>
        <span className="mx-2 text-line-strong" aria-hidden="true">
          ·
        </span>
        <a
          href={githubRepoUrl}
          target="_blank"
          rel="noopener noreferrer"
          aria-label="GitHub repository"
          title="GitHub"
          className="inline-flex transition-colors hover:text-oxblood"
        >
          <GitHubIcon />
        </a>
      </p>
    </footer>
  )
}
