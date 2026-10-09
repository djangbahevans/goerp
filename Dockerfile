FROM node:24-alpine AS shell
WORKDIR /src/shell
COPY shell/package.json shell/package-lock.json ./
COPY shell/packages/sdk/package.json ./packages/sdk/package.json
COPY shell/packages/app/package.json ./packages/app/package.json
COPY shell/packages/storybook/package.json ./packages/storybook/package.json
RUN npm ci --no-audit --no-fund
COPY shell/ ./
RUN npm run build

FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/engine ./cmd/engine

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/engine /usr/local/bin/engine
COPY --from=shell /src/shell/packages/app/dist /opt/goerp/shell
ENV GOERP_SHELL_DIR=/opt/goerp/shell
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/engine"]
