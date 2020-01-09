FROM golang:1 as builder

WORKDIR /go/src/helm-builder
COPY . .

RUN go build -o /helm-builder

FROM alpine:3

ARG GCLOUD_VERSION=272.0.0
ARG HELM_VERSION=v3.0.0
ARG SOPS_VERSION=v3.5.0

RUN apk --update --no-cache add python tar openssl wget ca-certificates git bash curl
RUN mkdir -p /opt

# gcloud
RUN	wget -q https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-sdk-${GCLOUD_VERSION}-linux-x86_64.tar.gz && \
	tar -xvf google-cloud-sdk-${GCLOUD_VERSION}-linux-x86_64.tar.gz && \
	mv google-cloud-sdk /opt/google-cloud-sdk && \
	/opt/google-cloud-sdk/install.sh --usage-reporting=true --path-update=true && \
	rm -f google-cloud-sdk-${GCLOUD_VERSION}-linux-x86_64.tar.gz && \
	/opt/google-cloud-sdk/bin/gcloud components install --quiet kubectl && \
	rm -rf /opt/google-cloud-sdk/.install/.backup && \
	mv /opt/google-cloud-sdk/bin/kubectl.1.14 /opt/google-cloud-sdk/bin/kubectl && \
	rm /opt/google-cloud-sdk/bin/kubectl.*

# helm
RUN wget -q https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz && \
	tar -xvf helm-${HELM_VERSION}-linux-amd64.tar.gz && \
	cp linux-amd64/helm /opt/google-cloud-sdk/bin/ && \
	chmod a+x /opt/google-cloud-sdk/bin/helm && \
	rm -rf helm-${HELM_VERSION}-linux-amd64.tar.gz linux-amd64

ENV PATH=$PATH:/opt/google-cloud-sdk/bin

# sops
ADD https://github.com/mozilla/sops/releases/download/${SOPS_VERSION}/sops-${SOPS_VERSION}.linux /opt/sops
RUN chmod u+x /opt/sops
RUN helm plugin install https://github.com/futuresimple/helm-secrets

# helm builder
COPY --from=builder /helm-builder /opt/google-cloud-sdk/bin/

ENTRYPOINT ["/opt/google-cloud-sdk/bin/helm-builder"]
