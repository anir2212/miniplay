# One image per Go command: docker build -f deploy/Dockerfile --build-arg SERVICE=score-svc .
# The gateway variant built locally on 11 November 2026. Other service variants,
# the CI matrix and a cluster install have not run; see RESULTS.md.
ARG GO_IMAGE=golang:1.27-alpine
ARG NODE_IMAGE=node:26-alpine
ARG RUNTIME_IMAGE=alpine:3.22

FROM ${NODE_IMAGE} AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM ${GO_IMAGE} AS build
ARG SERVICE
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN test -n "${SERVICE}" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app "./cmd/${SERVICE}"

FROM ${RUNTIME_IMAGE}
RUN addgroup -S -g 10001 miniplay && adduser -S -u 10001 -G miniplay miniplay
WORKDIR /app
COPY --from=build /out/app /app/app
# The gateway serves the web pages and migrate reads the SQL files. Other commands ignore both.
COPY --from=web /src/web/dist /app/web/dist
COPY db/migrations /app/db/migrations
USER 10001:10001
ENTRYPOINT ["/app/app"]
