# Builds a small production image of the API.
# Build from the repo root:  docker build -f deploy/Dockerfile -t hotspot-api .

# ---- build stage: compile the Go binary ----
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---- run stage: only the binary, no compiler or shell ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]