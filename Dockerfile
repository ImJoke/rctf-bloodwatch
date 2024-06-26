FROM golang:1.16 as builder

WORKDIR /go/src/rctf-bloodwatch
COPY go.mod go.sum ./

RUN go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 go build -v

FROM gcr.io/distroless/static:latest as run

COPY --from=builder /go/src/rctf-bloodwatch/rctf-bloodwatch .

CMD ["/rctf-bloodwatch", "--rctf-url", "ur rCTF url", "--token", "<ur rCTF token>", "--discord-webhook", "<ur web hook>", "--bot-name", "<ur bot name>"]
