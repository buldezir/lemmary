/**
 * Click-to-enlarge for the docs screenshots.
 *
 * Two states rather than one: the captures are 2800px wide in a ~700px column,
 * and fitting one to the viewport still shrinks a ~4000px page detail past
 * legibility, so the overlay also toggles to the capture's actual pixel size in
 * a scrollable pane. Arrow keys walk the page's images.
 *
 * No dependency: medium-zoom, the usual recipe, only does the fitted half.
 */

type Zoom = 'fit' | 'actual'

type Overlay = {
  root: HTMLDivElement
  img: HTMLImageElement
  pane: HTMLDivElement
  caption: HTMLParagraphElement
  counter: HTMLSpanElement
  zoomButton: HTMLButtonElement
  original: HTMLAnchorElement
}

let overlay: Overlay | null = null
let images: HTMLImageElement[] = []
let index = -1
let zoom: Zoom = 'fit'
/** The current image at full size, in CSS pixels. */
let full: { width: number; height: number } | null = null
/** The image that opened the overlay, so focus can go back to it. */
let opener: HTMLElement | null = null

const ZOOMABLE = 'data-lemmary-zoomable'

function icon(paths: string[], size = 18) {
  return `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none"
    stroke="currentColor" stroke-width="1.8" stroke-linecap="round"
    stroke-linejoin="round" aria-hidden="true">${paths
      .map((d) => `<path d="${d}" />`)
      .join('')}</svg>`
}

const closeIcon = icon(['M6 6 18 18', 'M18 6 6 18'])
const prevIcon = icon(['M15 5 8 12l7 7'])
const nextIcon = icon(['M9 5l7 7-7 7'])

function build(): Overlay {
  const root = document.createElement('div')
  root.className = 'lb'
  root.setAttribute('role', 'dialog')
  root.setAttribute('aria-modal', 'true')
  root.setAttribute('aria-label', 'Screenshot viewer')
  root.tabIndex = -1
  root.innerHTML = `
    <div class="lb-bar">
      <p class="lb-caption"></p>
      <div class="lb-actions">
        <span class="lb-counter"></span>
        <button type="button" class="lb-btn lb-zoom"></button>
        <a class="lb-btn lb-original" target="_blank" rel="noopener">Open original</a>
        <button type="button" class="lb-btn lb-icon lb-close" aria-label="Close (Esc)"
          title="Close (Esc)">${closeIcon}</button>
      </div>
    </div>
    <div class="lb-pane">
      <img class="lb-img" alt="" />
    </div>
    <button type="button" class="lb-nav lb-prev" aria-label="Previous screenshot"
      title="Previous (←)">${prevIcon}</button>
    <button type="button" class="lb-nav lb-next" aria-label="Next screenshot"
      title="Next (→)">${nextIcon}</button>
  `

  const made: Overlay = {
    root,
    img: root.querySelector('.lb-img') as HTMLImageElement,
    pane: root.querySelector('.lb-pane') as HTMLDivElement,
    caption: root.querySelector('.lb-caption') as HTMLParagraphElement,
    counter: root.querySelector('.lb-counter') as HTMLSpanElement,
    zoomButton: root.querySelector('.lb-zoom') as HTMLButtonElement,
    original: root.querySelector('.lb-original') as HTMLAnchorElement,
  }

  // The backdrop closes; the image and the bar do not.
  root.addEventListener('click', (event) => {
    if (event.target === root || event.target === made.pane) close()
  })
  made.img.addEventListener('click', () => setZoom(zoom === 'fit' ? 'actual' : 'fit'))
  made.zoomButton.addEventListener('click', () =>
    setZoom(zoom === 'fit' ? 'actual' : 'fit'),
  )
  ;(root.querySelector('.lb-close') as HTMLButtonElement).addEventListener('click', close)
  ;(root.querySelector('.lb-prev') as HTMLButtonElement).addEventListener('click', () =>
    step(-1),
  )
  ;(root.querySelector('.lb-next') as HTMLButtonElement).addEventListener('click', () =>
    step(1),
  )

  document.body.appendChild(root)
  return made
}

function setZoom(next: Zoom) {
  if (!overlay) return
  zoom = next
  overlay.img.classList.toggle('is-actual', zoom === 'actual')
  overlay.pane.classList.toggle('is-actual', zoom === 'actual')
  overlay.zoomButton.textContent = zoom === 'fit' ? 'Full size' : 'Fit to screen'
  overlay.img.title = zoom === 'fit' ? 'Click for full size' : 'Click to fit the screen'
  if (zoom === 'fit') {
    overlay.pane.scrollTo({ top: 0, left: 0 })
  } else {
    // Land on the top centre of the capture rather than its top-left corner.
    overlay.pane.scrollTop = 0
    overlay.pane.scrollLeft = (overlay.pane.scrollWidth - overlay.pane.clientWidth) / 2
  }
}

/**
 * The screenshots are captured at a device pixel ratio of 2, so half the
 * intrinsic size is the interface at the size it really has. A smaller image is
 * not a hidpi capture and is shown at its own size rather than halved.
 */
function fullSize(image: HTMLImageElement) {
  const width = image.naturalWidth || 0
  const height = image.naturalHeight || 0
  if (width === 0 || height === 0) return null
  const scale = width >= 1600 ? 2 : 1
  return { width: width / scale, height: height / scale }
}

/**
 * On a screen roomy enough for the capture, the fitted view already is full
 * size (nothing is upscaled), and a button that does nothing reads as broken.
 */
function updateZoomAffordance() {
  if (!overlay || !full) return
  const pane = overlay.pane
  const fitsWhole =
    full.width <= pane.clientWidth && full.height <= pane.clientHeight
  overlay.root.classList.toggle('is-fitting-whole', fitsWhole)
  if (fitsWhole && zoom === 'actual') setZoom('fit')
}

function show(next: number) {
  if (!overlay) return
  const image = images[next]
  if (!image) return
  index = next
  full = fullSize(image)
  overlay.root.style.setProperty('--lb-full-width', `${full?.width ?? 1400}px`)
  overlay.img.src = image.currentSrc || image.src
  overlay.img.alt = image.alt
  overlay.caption.textContent = image.alt
  overlay.original.href = image.currentSrc || image.src
  overlay.counter.textContent = `${next + 1} / ${images.length}`
  overlay.counter.hidden = images.length < 2
  overlay.root.classList.toggle('is-single', images.length < 2)
  setZoom('fit')
  updateZoomAffordance()
}

function step(delta: number) {
  if (images.length < 2) return
  show((index + delta + images.length) % images.length)
}

function onKeyDown(event: KeyboardEvent) {
  switch (event.key) {
    case 'Escape':
      close()
      break
    case 'ArrowLeft':
      step(-1)
      break
    case 'ArrowRight':
      step(1)
      break
    case 'z':
    case 'Z':
      if (overlay?.root.classList.contains('is-fitting-whole')) return
      setZoom(zoom === 'fit' ? 'actual' : 'fit')
      break
    default:
      return
  }
  event.preventDefault()
}

function open(image: HTMLImageElement) {
  overlay ??= build()
  opener = image
  overlay.root.classList.add('is-open')
  document.documentElement.classList.add('lb-locked')
  index = images.indexOf(image)
  // After is-open, so the pane has a measurable size for updateZoomAffordance.
  show(index < 0 ? 0 : index)
  document.addEventListener('keydown', onKeyDown)
  window.addEventListener('resize', updateZoomAffordance)
  overlay.root.focus()
}

function close() {
  if (!overlay) return
  overlay.root.classList.remove('is-open')
  document.documentElement.classList.remove('lb-locked')
  document.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('resize', updateZoomAffordance)
  // Free the decoded bitmap; some of these are 12 megapixels.
  overlay.img.removeAttribute('src')
  opener?.focus()
  opener = null
}

/**
 * Wires every content image on the current page. Idempotent, so it can run on
 * each route change without stacking listeners.
 */
export function mountLightbox() {
  if (typeof document === 'undefined') return

  images = Array.from(
    document.querySelectorAll<HTMLImageElement>('.vp-doc img'),
  ).filter((image) => !image.closest('a'))

  if (images.length === 0) {
    close()
    return
  }

  for (const image of images) {
    if (image.hasAttribute(ZOOMABLE)) continue
    image.setAttribute(ZOOMABLE, '')
    image.setAttribute('role', 'button')
    image.tabIndex = 0
    image.title = 'Click to enlarge'
    image.addEventListener('click', () => open(image))
    image.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault()
        open(image)
      }
    })
  }
}
