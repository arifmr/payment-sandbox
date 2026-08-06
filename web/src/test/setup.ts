import '@testing-library/jest-dom/vitest'
import { afterEach, beforeEach, vi } from 'vitest'
import { cleanup } from '@testing-library/react'
import { installMemoryStorage } from './localStorage'

/**
 * Global test setup.
 *
 * Each test gets a clean DOM, fresh storage and fresh mocks. Without the reset, a persisted
 * session from one test leaks into the next and the failure looks unrelated to whatever
 * actually changed.
 */

// Installed once here rather than per test file; see ./localStorage.ts for why a polyfill
// is needed at all on Node 26.
installMemoryStorage()

beforeEach(() => {
  window.localStorage.clear()
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
