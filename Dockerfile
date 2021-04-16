FROM golang:alpine

WORKDIR /code
ADD . .

RUN --mount=type=cache,target=/go/pkg/mod \
	  --mount=type=cache,target=/root/.cache/go-build \
    go build -o /exporter .

FROM alpine

COPY --from=0 /exporter /exporter

ENTRYPOINT [ "/exporter" ]
