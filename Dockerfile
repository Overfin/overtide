# Build a static binary, then ship it alone in a minimal image.
#
# docker build -t overtide .
# The build runs on the builder's own platform and cross-compiles for the
# target, so images for other architectures build without emulation. The
# web assets are built and committed (go run ./tools/buildassets), so the
# image needs no Node.
FROM --platform=$BUILDPLATFORM golang:1.27.0@sha256:4013ae0f9e7994f8535c58c811f8f863fbed38b72e0d51e6592156f758d66146 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/overfin/overtide/internal/product.Version=${VERSION}" -o /overtide ./cmd/overtide
# The final image has no shell, so the config folder is prepared here.
RUN mkdir -p /config/state

# distroless/static: no shell, no package manager, runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /overtide /overtide
# 65532 is the distroless "nonroot" user the container runs as; compose may
# run it as the user owning the media instead (user: in compose.yaml).
COPY --from=build --chown=65532:65532 /config /config
VOLUME /config
EXPOSE 8088
ENTRYPOINT ["/overtide"]
CMD ["-config", "/config/config.json"]
