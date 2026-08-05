import { createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
import { RootLayout } from './components/RootLayout'
import { IndexPage } from './routes/index'
import { UploadPage } from './routes/upload'
import { DocumentDetailPage } from './routes/document.$documentId'
import { DocumentAskPage } from './routes/document.$documentId.ask'
import { OCRTestPage } from './routes/ocr-test'
import { SearchPage } from './routes/search'
import { SettingsPage } from './routes/settings'
import { MailPage } from './routes/mail'

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

const mailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mail',
  component: MailPage,
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
  uploadRoute,
  searchRoute,
  ocrTestRoute,
  settingsRoute,
  mailRoute,
  documentRoute,
  documentAskRoute,
])

export const router = createRouter({ routeTree })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
