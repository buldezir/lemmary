import { inBrowser, useRoute, type Theme } from 'vitepress'
import DefaultTheme from 'vitepress/theme'
import { nextTick, watch } from 'vue'
import { mountLightbox } from './lightbox'
import './lightbox.css'

/**
 * The default theme plus click-to-enlarge on content images, which the
 * screenshots page needs to be worth reading. See lightbox.ts.
 */
export default {
  extends: DefaultTheme,
  setup() {
    if (!inBrowser) {
      return
    }
    // Client-side navigation swaps the article without remounting the app, so
    // the images are re-wired per route rather than once on load. mountLightbox
    // is idempotent.
    const route = useRoute()
    watch(
      () => route.path,
      () => void nextTick(mountLightbox),
      { immediate: true },
    )
  },
} satisfies Theme
