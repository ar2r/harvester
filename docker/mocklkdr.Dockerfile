FROM --platform=$BUILDPLATFORM golang:alpine AS builder
WORKDIR /src
ADD . .
RUN go build -o /out/mocklkdr ./cmd/mocklkdr

FROM alpine:latest
COPY --from=builder /out/mocklkdr /usr/local/bin/mocklkdr
EXPOSE 8080
ENTRYPOINT ["mocklkdr"]
