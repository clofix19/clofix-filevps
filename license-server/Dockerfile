FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/clofix-license ./cmd/clofix-license
FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/clofix-license /usr/local/bin/clofix-license
EXPOSE 8090
CMD ["clofix-license"]
