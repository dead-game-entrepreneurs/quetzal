FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/quetzal ./cmd/quetzal

FROM scratch
COPY --from=build /out/quetzal /quetzal
COPY third_party/licenses/Go-LICENSE.txt /licenses/Go-LICENSE.txt
USER 1000:1000
EXPOSE 8082/tcp 30120/udp 30121/udp 10100/udp 10105/udp 10110/udp 30200/udp 30201/udp 8124/udp 8122/udp 30060/udp 30061/udp 30600-30727/udp 30280/udp 30281/udp 13015/udp 13016/udp 30800-30927/udp 30380/udp 30381/udp
ENTRYPOINT ["/quetzal"]
