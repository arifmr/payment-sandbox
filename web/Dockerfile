# syntax=docker/dockerfile:1

# ---------- build ----------
# Node 22 LTS rather than the host's Node 26. Vite 7 supports both, but pinning to LTS
# keeps the build reproducible and sidesteps the experimental-global surprises newer
# Node releases carry (agent_documentation/07-frontend.md section 3 documents one that
# already bit the test suite).
FROM node:22-alpine AS build

WORKDIR /app

# Manifests first so `npm ci` is cached and only re-runs when dependencies actually
# change — editing a component then costs a bundle, not a full reinstall.
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

COPY . .

# `npm run build` is `tsc -b && vite build`, so a type error fails the image build
# rather than shipping a broken bundle. devDependencies are required for this and are
# present because `npm ci` installs them by default.
#
# No VITE_API_BASE_URL is set: the client falls back to '' (src/api/client.ts), which
# makes every request relative — /api/v1/... against whatever origin serves the app.
# nginx then proxies that to the backend, so the browser stays single-origin.
RUN npm run build

# ---------- runtime ----------
FROM nginx:alpine

# Replaces the stock default.conf: SPA fallback, asset caching, and the /api proxy.
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

# Only the built assets ship. No node_modules, no source, no toolchain — the runtime
# image is a static file server and nothing else.
EXPOSE 80
