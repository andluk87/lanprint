FROM golang:1.24.7-bookworm@sha256:b8bae5bd9ba9b1f89b635c91c24cc75cea335a16fb5076310f38566fc674b1ec AS build
WORKDIR /src
ENV GOTOOLCHAIN=local CGO_ENABLED=0
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go test -race -count=1 -v ./...
RUN mkdir -p /out && GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags="-s -w -H=windowsgui -buildid=" -o /out/LANPrint.exe ./cmd/lanprint
RUN cd /out && sha256sum LANPrint.exe > LANPrint.exe.sha256

FROM scratch AS artifact
COPY --from=build /out/ /
