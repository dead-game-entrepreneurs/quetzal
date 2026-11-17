FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/quetzal ./cmd/quetzal

FROM scratch
COPY --from=build /out/quetzal /quetzal
USER 1000:1000
EXPOSE 8082/tcp 30120/udp 30121/udp 10100/udp 10105/udp 10110/udp
ENTRYPOINT ["/quetzal"]
