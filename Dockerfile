FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /readest-hardcover-sync ./cmd/readest-hardcover-sync

FROM alpine:3.23
RUN adduser -D -s /bin/false app
USER app
WORKDIR /app
COPY --from=build /readest-hardcover-sync /usr/local/bin/
COPY static/ /app/static/
EXPOSE 8080
ENTRYPOINT ["readest-hardcover-sync"]
