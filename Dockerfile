FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/engine ./cmd/engine

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/engine /usr/local/bin/engine
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/engine"]
