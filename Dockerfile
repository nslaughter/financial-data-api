# The API's container image: a static server binary, with the fixtures of its
# contract version built in, on an empty base image. The release workflow
# reads the stage label to choose the conformance suite it runs against the
# image, and checks the contract version label against GET /v1/meta.
#
# The binary includes Go's standard library, whose BSD license requires its
# notice to accompany the binary, so the image carries Go's LICENSE as well
# as this project's.

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/server ./cmd/server

FROM scratch
COPY --from=build /out/server /server
COPY LICENSE /LICENSE
COPY --from=build /usr/local/go/LICENSE /licenses/go/LICENSE
LABEL org.opencontainers.image.title="Financial data API" \
      org.opencontainers.image.description="A demonstration REST API serving a synthetic financial dataset with its revision history." \
      org.opencontainers.image.source="https://github.com/nslaughter/financial-data-api" \
      org.opencontainers.image.licenses="LicenseRef-NSPUL-1.0 AND BSD-3-Clause" \
      com.nathanslaughter.financial-data-api.contract-version="0.3.0" \
      com.nathanslaughter.financial-data-api.stage="2"
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/server"]
