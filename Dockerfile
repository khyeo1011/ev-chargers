# One Dockerfile builds both binaries:
#   docker build --build-arg CMD=api    -t ev-chargers-api:dev .
#   docker build --build-arg CMD=worker -t ev-chargers-worker:dev .

# ---- build stage: full Go toolchain ----
FROM golang:1-alpine AS build
WORKDIR /src

# Copy module files first so the dependency download is cached
# and only re-runs when go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG CMD=api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${CMD}

# ---- runtime stage: no shell, no package manager, runs as non-root ----
# Ends up around 15 MB vs ~300 MB for the build image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app"]
