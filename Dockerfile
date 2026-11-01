ARG GO_VERSION=1.26.5
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/rate-limiter ./cmd/server

FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/rate-limiter /usr/local/bin/rate-limiter
EXPOSE 8080
ENTRYPOINT ["rate-limiter"]
CMD ["-addr", ":8080", "-algo", "token", "-limit", "10", "-rate", "1"]