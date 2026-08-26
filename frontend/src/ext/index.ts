import { emptyEdition, type Edition } from '../lib/edition'

/**
 * The open-source edition: no extra routes, no extra navigation.
 *
 * A private build shadows this module through the `@ext` alias rather than
 * editing it — see `vite.config.ts` and the `@ext` entry in `tsconfig.app.json`.
 */
export const edition: Edition = emptyEdition
