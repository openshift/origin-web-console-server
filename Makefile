all: build
.PHONY: all

build:
	go build -o _output/bin/origin-web-console-server github.com/openshift/origin-web-console-server/cmd/origin-web-console
.PHONY: build

build-image: build
	hack/build-image.sh
.PHONY: build-image

verify: build
	go test github.com/openshift/iorigin-web-console-server/pkg/...
.PHONY: verify

clean:
	rm -rf _output
.PHONY: clean