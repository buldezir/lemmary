import { createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
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
  component: SettingsPage,
})

const managementRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/management',
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
