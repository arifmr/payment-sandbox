/// <reference types="vite/client" />

/**
 * Typed environment variables. Declaring them means a typo in `import.meta.env.VITE_*`
 * is a compile error rather than an undefined at runtime.
 */
interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
