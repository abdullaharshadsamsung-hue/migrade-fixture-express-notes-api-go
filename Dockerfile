FROM golang:1.25-alpine

WORKDIR /app

COPY . .

RUN mkdir -p /app/bin && cd /app && go mod download && go build -o /app/bin/server ./cmd/server

EXPOSE 8080

CMD ["sh", "-c", "/app/bin/server"]
