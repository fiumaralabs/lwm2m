FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /lwm2md ./cmd/lwm2md

FROM alpine:3.22
RUN addgroup -S -g 10001 lwm2m && adduser -S -D -H -u 10001 -G lwm2m lwm2m \
    && mkdir /data && chown 10001:10001 /data
COPY --from=build /lwm2md /usr/local/bin/lwm2md
USER 10001:10001
VOLUME ["/data"]
EXPOSE 8080/tcp 8081/tcp 5683/udp 5684/udp 5783/udp 5784/udp
ENTRYPOINT ["/usr/local/bin/lwm2md"]
