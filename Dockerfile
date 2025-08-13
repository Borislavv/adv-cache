FROM gitreg.xbet.lan/web/main/ops/images/golang_1_23_toolkit:1.0.1 as prebuilder

ARG GO_GOOS=linux
ARG GO_GOARCH=amd64

ENV GO111MODULE=on \
    CGO_ENABLED=0 \
    GOOS=${GO_GOOS} \
    GOARCH=${GO_GOARCH} \
    GOPRIVATE="gitlab.xbet.lan"

RUN apt-get update && apt-get install -y \
    wget \
    unzip \
    curl \
    gnupg \
    gcc \
    g++ \
    libc6-dev \
    linux-headers-generic \
    make \
    g++-x86-64-linux-gnu \
    libc6-dev-amd64-cross \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY ./go.mod ./go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build,id=golang_skeleton_toolkit_go_mod_cache,uid=0,gid=0  \
    --mount=type=secret,id=netrc,target=/root/.netrc \
    go mod download

FROM prebuilder as builder

COPY . .

RUN go build -o advCache ./cmd/main.go

FROM gitreg.xbet.lan/web/main/ops/images/ubuntu_22_04:1 as prod_image

RUN addgroup --system --gid 1001 advCache && \
    adduser --system --home /advCache --uid 1001 --gid 1001 advCache

USER advCache
WORKDIR /advCache

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/advCache /advCache
COPY advCache.cfg.yaml .
EXPOSE 8001

ENTRYPOINT ["/advCache/advCache"]