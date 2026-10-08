# ---- Build: static Go binary ----
FROM golang:1.27-alpine AS build
WORKDIR /src
# Download modules first so this layer is cached until go.mod/go.sum change
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN go vet ./... \
 && go test ./... \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/statusboard .

# ---- Run: distroless, non-root, no shell ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/statusboard /statusboard
ENV PORT=8080 METRICS_PORT=9090
EXPOSE 8080 9090
# Numeric "nonroot" user so Kubernetes runAsNonRoot can verify it
USER 65532:65532
ENTRYPOINT ["/statusboard"]
