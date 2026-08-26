# Builds the creditchaind node binary only - not the website
# (cmd/staticserve is a separate, optional service; a peer node
# doesn't need to serve the website at all).
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/creditchaind .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/creditchaind /creditchaind
VOLUME ["/data"]
ENV DATA_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/creditchaind"]
