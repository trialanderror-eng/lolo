FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/lolo ./cmd/rca-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lolo /usr/local/bin/lolo
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/lolo"]
