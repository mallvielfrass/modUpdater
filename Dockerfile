FROM golang:1.27.1 AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/modupdater ./cmd/modupdater

FROM debian:bookworm-slim
RUN useradd --system --home /data --create-home modupdater
COPY --from=build /out/modupdater /usr/local/bin/modupdater
COPY server/config.example.json /config/config.json
USER modupdater
ENV MODUPDATER_LISTEN=:8080
ENV MODUPDATER_DATA_DIR=/data
EXPOSE 8080
ENTRYPOINT ["modupdater", "-config", "/config/config.json"]
