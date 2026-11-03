# Piyo API + importer. Multi-stage: static Go binaries on a distroless base.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/piyo-api ./cmd/piyo-api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/piyo-import ./cmd/piyo-import \
 && mkdir -p /src/releases  # a fresh clone has none (git-ignored); the image then starts without a dataset

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/piyo-api /out/piyo-import /app/
# Dataset releases built with piyo-import (backend/releases/, git-ignored) are baked into the image so the
# API can run on hosts without volumes (Cloud Run, Render, Koyeb). With Compose the /data/releases volume
# mounted over it wins. Map style and tiles stay volumes; without them /v1/map returns 404 and the app
# uses MAP_STYLE_URL.
COPY --from=build /src/releases /data/releases
ENV PORT=8080 RELEASES_DIR=/data/releases STYLE_DIR=/data/style TILES_DIR=/data/tiles
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/app/piyo-api"]
