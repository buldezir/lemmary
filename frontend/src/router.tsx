import { createRootRoute, createRoute, createRouter, redirect } from '@tanstack/react-router'
import { ensureAuth, isAdmin } from './lib/auth'
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
import { ExportPage } from './routes/export'

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

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
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

const searchRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/search',
  component: SearchPage,
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

const routeTree = rootRoute.addChildren([
  indexRoute,
  uploadRoute.addChildren([uploadFilesRoute, uploadAmazonRoute, uploadSplitRoute]),
  searchRoute,
  ocrTestRoute,
  settingsRoute,
  managementRoute,
  importRoute,
  exportRoute,
  documentRoute,
  documentAskRoute,
])

export const router = createRouter({ routeTree })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
