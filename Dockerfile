FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/engine ./cmd/engine

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/engine /usr/local/bin/engine
USER nobody
ENTRYPOINT ["/usr/local/bin/engine"]
