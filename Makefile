SERVICE_NAME := drone-gcloud-helm
REPOSITORY := github.com/lovoo/drone-cgloud-helm
DOCKER_IMAGE := docker.rz.lovoo.de/lovoo/drone-gcloud-helm
BUILD_ID := dev

all: build

test:
	@go test -v -race $(shell go list ${REPOSITORY}/... | grep -v /vendor/)

build:
	@go build -a --ldflags '-linkmode external -extldflags "-static"' -o build/${SERVICE_NAME}
	@echo "build OK"

docker-build:
	@rm -Rf build
	@docker run -v ${CURDIR}:/go/src/${REPOSITORY} -w "/go/src/${REPOSITORY}" golang:1.8 make

clean:
	@rm -Rf build
