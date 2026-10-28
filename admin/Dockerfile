# Build context is the repository root (admin imports the shared worker/pkg packages):
#   docker build -f admin/Dockerfile .
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY worker ./worker
COPY admin ./admin
WORKDIR /src/admin
RUN go get github.com/jackc/pgx/v5/pgxpool@v5.7.2 \
 && CGO_ENABLED=0 go build -trimpath -tags postgres -o /out/clofix-controller ./cmd/clofix-controller

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/clofix-controller /usr/local/bin/clofix-controller
COPY admin/web ./web
EXPOSE 8080 8443
CMD ["clofix-controller"]
