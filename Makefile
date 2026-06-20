SHELL := /bin/bash

GIT_SHORT_VERSION ?= $(shell git describe --tags --abbrev=8 --always)
GIT_LONG_VERSION ?= $(shell git describe --tags --abbrev=8 --dirty --always --long)
LDFLAGS ?= -w -s \
	-X 'edecan/internal/build.ShortVersion=$(GIT_SHORT_VERSION)' \
	-X 'edecan/internal/build.LongVersion=$(GIT_LONG_VERSION)'

GCFLAGS ?= -trimpath=$(PWD)
ASMFLAGS ?= -trimpath=$(PWD)

CI_EVENT ?= push

RELEASE_CHANNEL ?= $(shell git rev-parse --abbrev-ref HEAD)
COMMIT_TIMESTAMP = $(shell git show -s --format=%ct)
RELEASE_VERSION ?= $(shell TZ=Europe/Paris date -d "@$(COMMIT_TIMESTAMP)" +%Y.%-m.%-d)-$(RELEASE_CHANNEL).$(shell date -d "@${COMMIT_TIMESTAMP}" +%-H%M).$(shell git rev-parse --short HEAD)

GORELEASER_ARGS ?= release --snapshot --clean

watch: config.yaml .env tools/modd/bin/modd
	tools/modd/bin/modd

run: config.yaml .env generate
	go run ./cmd/edecan -env .env

run-with-env: .env
	( set -o allexport && source .env && set +o allexport && $(value CMD))

build: build-edecan

build-%: generate
	CGO_ENABLED=0 \
		go build \
			-ldflags "$(LDFLAGS)" \
			-gcflags "$(GCFLAGS)" \
			-asmflags "$(ASMFLAGS)" \
			-o ./bin/$* ./cmd/$*

test:
	go test ./...

vet:
	go vet ./...

purge:
	rm -rf *.db *.db-journal

generate: tools/templ/bin/templ
	tools/templ/bin/templ generate

bin/templ: tools/templ/bin/templ
	mkdir -p bin
	ln -fs $(PWD)/tools/templ/bin/templ bin/templ

tools/templ/bin/templ:
	mkdir -p tools/templ/bin
	GOBIN=$(PWD)/tools/templ/bin go install github.com/a-h/templ/cmd/templ@v0.3.1020

tools/modd/bin/modd:
	mkdir -p tools/modd/bin
	GOBIN=$(PWD)/tools/modd/bin go install github.com/cortesi/modd/cmd/modd@latest

tools/act/bin/act:
	mkdir -p tools/act/bin
	cd tools/act && curl https://raw.githubusercontent.com/nektos/act/master/install.sh | bash -

ci: tools/act/bin/act
	tools/act/bin/act $(CI_EVENT)

tools/goreleaser/bin/goreleaser:
	mkdir -p tools/goreleaser/bin
	GOBIN=$(PWD)/tools/goreleaser/bin go install github.com/goreleaser/goreleaser/v2@latest

goreleaser: tools/goreleaser/bin/goreleaser
	REPO_OWNER=$(shell whoami) tools/goreleaser/bin/goreleaser $(GORELEASER_ARGS)

config.yaml:
	cp config.yaml.dist config.yaml
