import {
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
  type SearchSchemaInput,
} from '@tanstack/react-router'
import { ensureAuth, isAdmin } from './lib/auth'
import {
  documentQuerySearch,
  inboxQuerySearch,
  parseDocumentQuery,
  type DocumentQueryInput,
} from './lib/documentQuery'
import { RootLayout } from './components/RootLayout'
import { IndexPage } from './routes/index'
import { InboxPage } from './routes/inbox'
import { ActivityPage } from './routes/activity'
import { UploadPage } from './routes/upload'
import { UploadFilesPage } from './routes/upload.index'
import { UploadScanPage } from './routes/upload.scan'
import { UploadAmazonPage } from './routes/upload.amazon'
import { UploadZipPage } from './routes/upload.zip'
import { UploadSplitPage } from './routes/upload.split'
import { DocumentDetailPage } from './routes/document.$documentId'
import { DocumentAskPage } from './routes/document.$documentId.ask'
import { OCRTestPage } from './routes/ocr-test'
import { SearchPage } from './routes/search'
import { ResearchPage } from './routes/research'
import { SettingsPage } from './routes/settings'
import { SettingsAppearancePage } from './routes/settings.index'
import { SettingsAIPage } from './routes/settings.ai'
import { SettingsProcessingPage } from './routes/settings.processing'
import { SettingsWorkerPage } from './routes/settings.worker'
import { SettingsDuplicatesPage } from './routes/settings.duplicates'
import { SettingsIngestPage } from './routes/settings.ingest'
import { MaintenancePage } from './routes/maintenance'
import { ImportPage } from './routes/import'
import { ImportNgxPage } from './routes/import.ngx'
import { ImportArchivePage } from './routes/import.archive'
import { ExportPage } from './routes/export'
import { AccountPage } from './routes/account'
import { TagsPage } from './routes/tags'

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

// validateSearch types the query-string filters on the way in and defaults
// away anything hand-edited.
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  // The SearchSchemaInput brand tells the router the filters are optional going
  // in though all are set coming out; without it every `to: '/'` would have to
  // spell out a full filter set.
  validateSearch: (search: DocumentQueryInput & SearchSchemaInput) =>
    documentQuerySearch(parseDocumentQuery(search)),
  component: IndexPage,
})

// See lib/nav.ts for why this is a path and not /?status=needs_review.
const inboxRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/inbox',
  validateSearch: (search: DocumentQueryInput & SearchSchemaInput) => inboxQuerySearch(search),
  component: InboxPage,
})

// Not behind Maintenance: the collection's list rule scopes it to the caller's
// own documents, so it is not an admin view.
const activityRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/activity',
  component: ActivityPage,
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

const uploadScanRoute = createRoute({
  getParentRoute: () => uploadRoute,
  path: 'scan',
  component: UploadScanPage,
})

const uploadAmazonRoute = createRoute({
  getParentRoute: () => uploadRoute,
  path: 'amazon',
  component: UploadAmazonPage,
})

const uploadZipRoute = createRoute({
  getParentRoute: () => uploadRoute,
  path: 'zip',
  component: UploadZipPage,
})

const uploadSplitRoute = createRoute({
  getParentRoute: () => uploadRoute,
  path: 'split',
  component: UploadSplitPage,
})

// Deep Search's two modes are two paths, not a flag: the mode decides what a
// question does, so it belongs in the URL. Both render SearchPage, which reads
// the mode back off the route. They share the /rag parent so one nav entry
// marks itself active for both; /rag has no component of its own.
const ragRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/rag',
})

// /rag alone is not a page; Search is the cheaper mode to land on.
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

// The chat id is a child route that renders nothing, so SearchPage stays on
// the parent match: promoting /rag/search to /rag/search/<id> mid-send would
// otherwise swap the match and unmount the transcript. The page reads the id
// with useMatchRoute, and no <Outlet/> is rendered.
const searchSessionRoute = createRoute({
  getParentRoute: () => searchRoute,
  path: '$sessionId',
  component: () => null,
})

const researchRoute = createRoute({
  getParentRoute: () => ragRoute,
  path: 'research',
  component: ResearchPage,
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

// A shell with one tab per section; the guard sits on the parent, which the
// tabs inherit.
const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  beforeLoad: requireAdmin,
  component: SettingsPage,
})

// Appearance is the first tab, so it sits on /settings itself.
const settingsAppearanceRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: '/',
  component: SettingsAppearancePage,
})

const settingsAIRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'ai',
  component: SettingsAIPage,
})

const settingsProcessingRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'processing',
  component: SettingsProcessingPage,
})

const settingsWorkerRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'worker',
  component: SettingsWorkerPage,
})

const settingsDuplicatesRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'duplicates',
  component: SettingsDuplicatesPage,
})

const settingsIngestRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'ingest',
  component: SettingsIngestPage,
})

const maintenanceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/maintenance',
  beforeLoad: requireAdmin,
  component: MaintenancePage,
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
// and unlike /settings and /maintenance this page has to work for non-admins.
const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/account',
  component: AccountPage,
})

// No guard, same as accountRoute: tags are per-user, not an admin setting.
const tagsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/tags',
  component: TagsPage,
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
  inboxRoute,
  activityRoute,
  uploadRoute.addChildren([uploadFilesRoute, uploadScanRoute, uploadAmazonRoute, uploadZipRoute, uploadSplitRoute]),
  ragRoute.addChildren([
    ragIndexRoute,
    searchRoute.addChildren([searchSessionRoute]),
    researchRoute.addChildren([researchSessionRoute]),
  ]),
  ocrTestRoute,
  settingsRoute.addChildren([
    settingsAppearanceRoute,
    settingsAIRoute,
    settingsProcessingRoute,
    settingsWorkerRoute,
    settingsDuplicatesRoute,
    settingsIngestRoute,
  ]),
  maintenanceRoute,
  importRoute.addChildren([importArchiveRoute, importNgxRoute, importArchiveAliasRoute]),
  exportRoute,
  accountRoute,
  tagsRoute,
  documentRoute,
  documentAskRoute.addChildren([documentAskSessionRoute]),
])

export const router = createRouter({ routeTree })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
