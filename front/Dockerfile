FROM node:24.21.0-alpine AS build
WORKDIR /src
COPY package.json package-lock.json .npmrc ./
RUN npm ci
COPY . .
RUN npm run build

# The built page is static: nginx serves it, forwards /api to mcpd, and serves
# the manuals' own pages, so the browser sees one origin and needs no network.
FROM nginx:1.30.4-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
