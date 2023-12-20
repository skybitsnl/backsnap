FROM golang:1.21.5-bookworm AS build

WORKDIR /app
COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY pkg ./pkg

RUN cd cmd/backsnap && go build -o /backsnap

FROM ubuntu:jammy-20230522 AS base
RUN apt-get update && apt-get --no-install-recommends install -y \
	wget ca-certificates tzdata \
	&& rm -rf /var/lib/apt/lists/*
WORKDIR /
EXPOSE 80

FROM base AS backsnap
COPY --from=build /backsnap /backsnap
USER root:root
ENTRYPOINT ["/backsnap"]
