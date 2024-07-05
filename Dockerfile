FROM golang:1.22 as build

WORKDIR /build

COPY . .

RUN CGO_ENABLED=0 go build -o /main .

FROM gcr.io/distroless/static-debian11

COPY --from=build /main .

CMD ["./main"]
