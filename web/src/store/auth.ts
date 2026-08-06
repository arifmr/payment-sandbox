import { create } from 'zustand'
import { persist, createJSONStorage } from 'zustand/middleware'
import { api, type Tokens } from '@/api/client'
import { auth as authApi } from '@/api/endpoints'
import type { LoginResponse, Role, User } from '@/api/types'

/**
 * Session state.
 *
 * SRS §4.1 asks for tidy state management. Zustand is used rather than Redux because the
 * only genuinely global state here is the session — everything else is server data, which
 * lives in the component that requests it (see `useAsync`). Reaching for Redux would mean
 * a store, actions and reducers for one object.
 *
 * **Storage trade-off, stated plainly:** tokens live in localStorage so a reload does not
 * log the user out. That means any XSS on this origin can read them. The alternative —
 * httpOnly cookies — cannot be done from the client alone; the backend would have to set
 * them and add CSRF protection. Given the sandbox scope, persistence was chosen and the
 * exposure is documented rather than hidden. Mitigation that *is* in place: the access
 * token expires in 15 minutes, and refresh tokens are single-use with reuse detection, so
 * a stolen pair has a short and self-limiting life.
 */

interface AuthState {
  user: User | null
  accessToken: string | null
  refreshToken: string | null
  /** True while a login/register/logout call is in flight. */
  busy: boolean

  login: (email: string, password: string) => Promise<User>
  register: (name: string, email: string, password: string) => Promise<User>
  logout: () => Promise<void>
  /** Applies a rotated token pair. Called by the API client, not by the UI. */
  applyRefresh: (login: LoginResponse) => void
  /** Drops local state without calling the API. Used when the session is unrecoverable. */
  clear: () => void
}

const STORAGE_KEY = 'payment-sandbox.session'

export const useAuth = create<AuthState>()(
  persist(
    (set, get) => ({
      user: null,
      accessToken: null,
      refreshToken: null,
      busy: false,

      login: async (email, password) => {
        set({ busy: true })
        try {
          const res = await authApi.login({ email, password })
          set({
            user: res.user,
            accessToken: res.access_token,
            refreshToken: res.refresh_token,
          })
          return res.user
        } finally {
          set({ busy: false })
        }
      },

      register: async (name, email, password) => {
        set({ busy: true })
        try {
          // Register returns the user but no tokens, so log in immediately after to
          // avoid making the user type the same credentials twice.
          const created = await authApi.register({ name, email, password })
          await get().login(email, password)
          return created
        } finally {
          set({ busy: false })
        }
      },

      logout: async () => {
        const token = get().refreshToken
        set({ busy: true })
        try {
          // Revoke server-side so the refresh token cannot be replayed. Failure here
          // must not trap the user in a logged-in UI, so local state is cleared either
          // way — the token expires on its own regardless.
          if (token) await authApi.logout(token).catch(() => undefined)
        } finally {
          set({ user: null, accessToken: null, refreshToken: null, busy: false })
        }
      },

      applyRefresh: (login) =>
        set({
          user: login.user,
          accessToken: login.access_token,
          refreshToken: login.refresh_token,
        }),

      clear: () => set({ user: null, accessToken: null, refreshToken: null }),
    }),
    {
      name: STORAGE_KEY,
      // window.localStorage, not the bare global. Node 26 ships its own experimental
      // `localStorage` global that is unavailable unless --localstorage-file is passed,
      // and it shadows the one jsdom installs — so the bare reference works in a browser
      // but blows up under test. Going through `window` is unambiguous in both.
      storage: createJSONStorage(() => window.localStorage),
      // `busy` is transient; persisting it would leave a reloaded page stuck in a
      // loading state after a refresh mid-request.
      partialize: (state) => ({
        user: state.user,
        accessToken: state.accessToken,
        refreshToken: state.refreshToken,
      }),
    },
  ),
)

/**
 * Wires the store into the API client. Called once at bootstrap.
 *
 * The dependency points this way on purpose: the client knows nothing about Zustand, so
 * it stays testable on its own, and the store does not have to know about HTTP retries.
 */
export function connectAuthToApi(): void {
  api.attachSession({
    getTokens: (): Tokens | null => {
      const { accessToken, refreshToken } = useAuth.getState()
      if (!accessToken || !refreshToken) return null
      return { accessToken, refreshToken }
    },
    onRefreshed: (login) => useAuth.getState().applyRefresh(login),
    onSessionExpired: () => useAuth.getState().clear(),
  })
}

// ── selectors ─────────────────────────────────────────────────────────────────
// Narrow selectors so a component only re-renders when the slice it reads changes.

export const selectIsAuthenticated = (s: AuthState) => Boolean(s.accessToken && s.user)
export const selectRole = (s: AuthState): Role | null => s.user?.role ?? null

/** Where a role lands after logging in. */
export function homePathFor(role: Role | null): string {
  return role === 'ADMIN' ? '/admin' : '/merchant'
}
