FROM golang:1.20-alpine as builder

WORKDIR /nicoalert

COPY . ./

RUN go mod download
RUN go build -mod=readonly -v -o nicoalert

WORKDIR /nicoalert/sidecar

RUN go mod download
RUN go build -mod=readonly -v -o sidecar

FROM alpine

RUN apk add --update --no-cache netcat-openbsd

WORKDIR /nicoalert

COPY --from=builder /nicoalert/nicoalert /nicoalert/nicoalert
COPY --from=builder /nicoalert/sidecar/sidecar /nicoalert/sidecar

CMD ["/nicoalert/nicoalert"]
