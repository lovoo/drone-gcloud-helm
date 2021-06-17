FROM golang:1 as builder

WORKDIR /helm-builder
COPY . .

RUN CGO_ENABLED=0 go build -mod vendor -o helm-builder


FROM alpine:3

ARG GCLOUD_VERSION=345.0.0
ARG HELM_VERSION=v3.6.1

RUN apk --update --no-cache add python3 tar openssl wget ca-certificates
RUN mkdir -p /opt

RUN	wget -q https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-sdk-${GCLOUD_VERSION}-linux-x86_64.tar.gz && \
	tar -xvf google-cloud-sdk-${GCLOUD_VERSION}-linux-x86_64.tar.gz && \
	mv google-cloud-sdk /opt/google-cloud-sdk && \
	/opt/google-cloud-sdk/install.sh --usage-reporting=true --path-update=true && \
	rm -f google-cloud-sdk-${GCLOUD_VERSION}-linux-x86_64.tar.gz && \
	/opt/google-cloud-sdk/bin/gcloud components install --quiet kubectl && \
	rm -rf /opt/google-cloud-sdk/.install/.backup

RUN wget -q https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz && \
	tar -xvf helm-${HELM_VERSION}-linux-amd64.tar.gz && \
	cp linux-amd64/helm /opt/google-cloud-sdk/bin/ && \
	chmod a+x /opt/google-cloud-sdk/bin/helm && \
	rm -rf helm-${HELM_VERSION}-linux-amd64.tar.gz linux-amd64

# helm builder
COPY --from=builder /helm-builder/helm-builder /opt/google-cloud-sdk/bin/helm-builder

ENV PATH=$PATH:/opt/google-cloud-sdk/bin

ENTRYPOINT ["/opt/google-cloud-sdk/bin/helm-builder"]
