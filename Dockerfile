FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend-builder

WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o lemmary .

FROM node:26-alpine AS frontend-builder

WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm install
COPY frontend/ .
COPY docs/ /app/docs/
RUN npm run build

FROM alpine:3.21
LABEL org.opencontainers.image.title="Lemmary" \
      org.opencontainers.image.description="Source-available document storage with OCR and AI metadata extraction" \
      org.opencontainers.image.source="https://github.com/buldezir/lemmary" \
      org.opencontainers.image.licenses="PolyForm-Noncommercial-1.0.0"
RUN apk add --no-cache poppler-utils
WORKDIR /app
COPY --from=backend-builder /app/backend/lemmary /app/lemmary
COPY --from=frontend-builder /app/public /app/public
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

ENV PORT=80
EXPOSE ${PORT}
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q --spider "http://127.0.0.1:${PORT}/api/health" || exit 1
ENTRYPOINT ["/app/docker-entrypoint.sh"]
