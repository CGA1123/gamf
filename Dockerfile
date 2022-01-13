FROM golang:alpine AS gamfbuilder

WORKDIR /app

ADD https://github.com/CGA1123/gamf/archive/main.tar.gz /tmp/gamf.tgz
RUN tar -xvf /tmp/gamf.tgz

WORKDIR /app/gamf-main

RUN CGO_ENABLED=0 go build -o /gamf -ldflags '-extldflags "-static"' -tags timetzdata

FROM scratch

COPY --from=gamfbuilder /gamf /gamf

ENTRYPOINT ["/gamf"]
