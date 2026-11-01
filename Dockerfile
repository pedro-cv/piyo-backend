# Piyo API + importer. Multi-stage: static Go binaries on a distroless base.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/piyo-api ./cmd/piyo-api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/piyo-import ./cmd/piyo-import

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/piyo-api /out/piyo-import /app/
# Releases, map style and tiles are mounted volumes (see deploy/docker-compose.yml).
ENV PORT=8080 RELEASES_DIR=/data/releases STYLE_DIR=/data/style TILES_DIR=/data/tiles
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/app/piyo-api"]
