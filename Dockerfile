# syntax=docker/dockerfile:1

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/viewer-counter ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/viewer-counter /app/viewer-counter
COPY config.example.yaml /app/config.example.yaml
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/viewer-counter"]
CMD ["-config", "/app/config.yaml"]
