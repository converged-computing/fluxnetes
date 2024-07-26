FROM postgres:15.5-bookworm

ENV DEBIAN_FRONTEND=noninteractive
ENV GO_VERSION=1.22.5

RUN apt-get update && apt-get install -y wget && apt clean -y && apt -y autoremove

# Install go
RUN wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz  && tar -xvf go${GO_VERSION}.linux-amd64.tar.gz && \
    mv go /usr/local && rm go${GO_VERSION}.linux-amd64.tar.gz
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH
RUN go install github.com/riverqueue/river/cmd/river@latest && mv /root/go/bin/river /usr/local/bin
# On start need to run:
# runuser -l postgres -c '/usr/local/bin/river migrate-up --database-url postgres://localhost:5432/postgres'
