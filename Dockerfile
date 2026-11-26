FROM golang:1.27@sha256:5f1b98fa02fe396ebce2a0227a0e08309e11792915b9dd2e1f71053d7e7e6666 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /readest-hardcover-sync ./cmd/readest-hardcover-sync

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN adduser -D -s /bin/false app
USER app
WORKDIR /app
COPY --from=build /readest-hardcover-sync /usr/local/bin/
COPY static/ /app/static/
EXPOSE 8080
ENTRYPOINT ["readest-hardcover-sync"]
CMD ["serve"]
