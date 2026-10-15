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
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/campus-ai-gateway-migrate ./cmd/migrate

FROM alpine:3.21
RUN adduser -D -u 10001 gateway
USER gateway
COPY --from=build /out/campus-ai-gateway /usr/local/bin/campus-ai-gateway
COPY --from=build /out/campus-ai-gateway-migrate /usr/local/bin/campus-ai-gateway-migrate
# The single copy of the migration files the image ships with. The compose
# stack runs the migrate binary against these before the gateway starts.
COPY --from=build /src/migrations /migrations
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/campus-ai-gateway"]
