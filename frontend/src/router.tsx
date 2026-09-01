import {
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
  type SearchSchemaInput,
} from '@tanstack/react-router'
import { ensureAuth, isAdmin } from './lib/auth'
import { documentQuerySearch, parseDocumentQuery, type DocumentQueryInput } from './lib/documentQuery'
import { RootLayout } from './components/RootLayout'
import { IndexPage } from './routes/index'
import { UploadPage } from './routes/upload'
import { UploadFilesPage } from './routes/upload.index'
import { UploadAmazonPage } from './routes/upload.amazon'
import { UploadSplitPage } from './routes/upload.split'
import { DocumentDetailPage } from './routes/document.$documentId'
import { DocumentAskPage } from './routes/document.$documentId.ask'
import { OCRTestPage } from './routes/ocr-test'
import { SearchPage } from './routes/search'
import { SettingsPage } from './routes/settings'
import { ManagementPage } from './routes/management'
import { ImportPage } from './routes/import'
import { ImportNgxPage } from './routes/import.ngx'
import { ImportArchivePage } from './routes/import.archive'
import { ExportPage } from './routes/export'
import { AccountPage } from './routes/account'

// Admin-only routes bounce non-admins to the document list before the page
// component mounts. RootLayout still runs the login and setup gates.
async function requireAdmin() {
  try {
    await ensureAuth()
  } catch {
    throw redirect({ to: '/' })
  }
  if (!(await isAdmin())) {
    throw redirect({ to: '/' })
  }
}

const rootRoute = createRootRoute({
  component: RootLayout,
})

// The document list's filters live in the query string so a reload, a bookmark
// and a shared link all reproduce the same list. validateSearch is what makes
// them typed on the way in, and what defaults away anything hand-edited.
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  // The SearchSchemaInput brand is what tells the router that the filters are
  // optional going in even though every one of them is set coming out — without
  // it, every `to: '/'` in the app would have to spell out a full filter set.
  validateSearch: (search: DocumentQueryInput & SearchSchemaInput) =>
    documentQuerySearch(parseDocumentQuery(search)),
  component: IndexPage,
})

const uploadRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/upload',
  component: UploadPage,
})

// Plain file upload is the default source, so it sits on /upload itself.
const uploadFilesRoute = createRoute({
  getParentRoute: () => uploadRoute,
  path: '/',
  component: UploadFilesPage,
})

const uploadAmazonRoute = createRoute({
  getParentRoute: () => uploadRoute,
  path: 'amazon',
  component: UploadAmazonPage,
})

const uploadSplitRoute = createRoute({
  getParentRoute: () => uploadRoute,
  path: 'split',
  component: UploadSplitPage,
})

// Deep Search's two modes are two paths, not a flag on one: the mode decides
// what a question does -- list documents, or read them and answer -- so it
// belongs in the URL, where a reload, a bookmark and a shared link all keep it.
// Both render SearchPage, which reads the mode back off the route.
//
// They share the /rag parent so one nav entry covers both: a Link marks itself
// active for its own path and everything under it, and /rag is the only path
// that is above both modes. It has no component of its own, so it renders an
// Outlet and contributes nothing but the segment.
const ragRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/rag',
})

// /rag alone is not a page. Search is the cheaper of the two modes and the one
// to land on.
const ragIndexRoute = createRoute({
  getParentRoute: () => ragRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/rag/search' })
  },
})

const searchRoute = createRoute({
  getParentRoute: () => ragRoute,
  path: 'search',
  component: SearchPage,
})

// The open chat's id is a child route that renders nothing, and SearchPage
// stays on the parent match on purpose. Sending the first message of a new
// chat promotes /rag/search to /rag/search/<id> while the request is still in
// flight; with the page on a child (or on a sibling route) that promotion swaps
// the match and React unmounts the transcript mid-send. Here only a child match
// is added, and the page — which reads the id with useMatchRoute — keeps
// running. Neither this route nor its document twin renders an <Outlet/>.
const searchSessionRoute = createRoute({
  getParentRoute: () => searchRoute,
  path: '$sessionId',
  component: () => null,
})

const researchRoute = createRoute({
  getParentRoute: () => ragRoute,
  path: 'research',
  component: SearchPage,
})

const researchSessionRoute = createRoute({
  getParentRoute: () => researchRoute,
  path: '$sessionId',
  component: () => null,
})

const ocrTestRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/ocr-test',
  component: OCRTestPage,
})

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  beforeLoad: requireAdmin,
  component: SettingsPage,
})

const managementRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/management',
  beforeLoad: requireAdmin,
  component: ManagementPage,
})

const importRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/import',
  component: ImportPage,
})

// Lemmary archive is the default import source, so it sits on /import itself.
const importArchiveRoute = createRoute({
  getParentRoute: () => importRoute,
  path: '/',
  component: ImportArchivePage,
})

const importNgxRoute = createRoute({
  getParentRoute: () => importRoute,
  path: 'ngx',
  component: ImportNgxPage,
})

// /import/archive was the archive tab before it moved onto /import.
const importArchiveAliasRoute = createRoute({
  getParentRoute: () => importRoute,
  path: 'archive',
  beforeLoad: () => {
    throw redirect({ to: '/import' })
  },
})

// No beforeLoad guard on purpose: RootLayout's gate already requires a session,
// and unlike /settings and /management this page has to work for non-admins.
const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/account',
  component: AccountPage,
})

const exportRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/export',
  component: ExportPage,
})

const documentRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/document/$documentId',
  component: DocumentDetailPage,
})

const documentAskRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/document/$documentId/ask',
  component: DocumentAskPage,
})

// See searchSessionRoute: a placeholder child so the page survives the URL
// gaining a session id mid-send.
const documentAskSessionRoute = createRoute({
  getParentRoute: () => documentAskRoute,
  path: '$sessionId',
  component: () => null,
})

const routeTree = rootRoute.addChildren([
  indexRoute,
  uploadRoute.addChildren([uploadFilesRoute, uploadAmazonRoute, uploadSplitRoute]),
  ragRoute.addChildren([
    ragIndexRoute,
    searchRoute.addChildren([searchSessionRoute]),
    researchRoute.addChildren([researchSessionRoute]),
  ]),
  ocrTestRoute,
  settingsRoute,
  managementRoute,
  importRoute.addChildren([importArchiveRoute, importNgxRoute, importArchiveAliasRoute]),
  exportRoute,
  accountRoute,
  documentRoute,
  documentAskRoute.addChildren([documentAskSessionRoute]),
])

export const router = createRouter({ routeTree })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
