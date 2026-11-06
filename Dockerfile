FROM golang:1.27.3-alpine AS build

# Never download another toolchain: the image and go.mod must agree.
ENV GOTOOLCHAIN=local

ARG SERVICE
RUN test -n "${SERVICE}" || (echo "build arg SERVICE is required" >&2; exit 1)

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY internal ./internal
COPY services ./services
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./services/${SERVICE}/cmd

FROM gcr.io/distroless/static:nonroot

COPY --from=build /out/app /app
EXPOSE 8080
ENTRYPOINT ["/app"]