FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/remi-api ./cmd/api

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/remi-api /remi-api
EXPOSE 8080
ENTRYPOINT ["/remi-api"]
