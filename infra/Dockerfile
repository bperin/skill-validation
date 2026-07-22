FROM golang:1.26.5-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/project-validation ./cmd/project-validation

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/project-validation /project-validation
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/project-validation"]
