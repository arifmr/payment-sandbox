/**
 * In-memory Storage implementation for tests.
 *
 * Node 26 ships an experimental built-in `localStorage` that is unavailable unless the
 * process is started with `--localstorage-file`. That global takes precedence over the one
 * jsdom would install, so `window.localStorage` exists as a property but reading it yields
 * `undefined` and logs an ExperimentalWarning. Anything touching storage — here, the
 * persisted auth store — then fails for a reason that has nothing to do with the code
 * under test.
 *
 * Installing a real in-memory Storage sidesteps the whole question: no Node flag, no file
 * on disk, and state that is trivially resettable between tests.
 */
export function createMemoryStorage(): Storage {
  let store = new Map<string, string>()

  return {
    get length() {
      return store.size
    },
    clear() {
      store = new Map()
    },
    getItem(key: string) {
      // Storage returns null for a missing key, not undefined. Getting this wrong makes
      // JSON.parse(undefined) throw inside the persist middleware.
      return store.has(key) ? (store.get(key) as string) : null
    },
    key(index: number) {
      return Array.from(store.keys())[index] ?? null
    },
    removeItem(key: string) {
      store.delete(key)
    },
    setItem(key: string, value: string) {
      // The real Storage coerces to string; mirroring that keeps behaviour identical.
      store.set(key, String(value))
    },
  }
}

/**
 * Installs the storage on every reference the app might use.
 *
 * Both `globalThis` and `window` are patched because the app reaches for
 * `window.localStorage` while some libraries use the bare global, and in this environment
 * they are not guaranteed to resolve to the same object.
 */
export function installMemoryStorage(): Storage {
  const storage = createMemoryStorage()
  const target = globalThis as unknown as Record<string, unknown>

  // configurable/writable so a later test can replace it, and so re-installing does not
  // throw on an existing non-configurable property.
  Object.defineProperty(target, 'localStorage', {
    value: storage,
    configurable: true,
    writable: true,
  })
  if (typeof window !== 'undefined') {
    Object.defineProperty(window, 'localStorage', {
      value: storage,
      configurable: true,
      writable: true,
    })
  }
  return storage
}
