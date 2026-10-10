FROM node:22-alpine AS admin-ui
WORKDIR /src/web/admin
COPY web/admin/package.json ./
RUN npm install --no-audit --no-fund
COPY web/admin ./
RUN npm run build

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
COPY --from=admin-ui /src/internal/app/adminui/dist ./internal/app/adminui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/campus-ai-gateway ./cmd/server

FROM alpine:3.21
RUN adduser -D -u 10001 gateway
USER gateway
COPY --from=build /out/campus-ai-gateway /usr/local/bin/campus-ai-gateway
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/campus-ai-gateway"]
