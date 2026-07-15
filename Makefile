BINARY_NAME := awg-server
DIST_DIR := dist

PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

VERSION ?= dev
RELEASE_PUBLIC_KEY ?=
LDFLAGS := -s -w -X main.version=$(VERSION)
ifneq ($(RELEASE_PUBLIC_KEY),)
LDFLAGS += -X github.com/stealthsurf-vpn/awg-server/internal/update.releasePublicKey=$(RELEASE_PUBLIC_KEY)
endif

.PHONY: build build-all clean vet

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) .

build-all: clean
	@mkdir -p $(DIST_DIR)
	@$(foreach platform,$(PLATFORMS), \
		$(eval OS := $(word 1,$(subst /, ,$(platform)))) \
		$(eval ARCH := $(word 2,$(subst /, ,$(platform)))) \
		$(eval EXT := $(if $(filter windows,$(OS)),.exe,)) \
		echo "Building $(OS)/$(ARCH)..." && \
		CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -trimpath \
			-ldflags="$(LDFLAGS)" \
			-o $(DIST_DIR)/$(BINARY_NAME)-$(OS)-$(ARCH)$(EXT) . && \
	) true

clean:
	rm -rf $(DIST_DIR)

vet:
	go vet ./...
