FROM golang:1.23

WORKDIR /app

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

WORKDIR /app/cmd/server

RUN go build -o /grpc-chat-server

EXPOSE 8080

CMD ["/grpc-chat-server"]
