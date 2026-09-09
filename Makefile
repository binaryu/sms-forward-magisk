APP ?= sms-tg-forwarder
MODULE ?= sms-tg-forwarder
DIST_DIR ?= dist
TARGET ?= $(DIST_DIR)/$(APP)-linux-arm64
MODULE_DIR ?= sms_tg_forwarder
MODULE_ZIP ?= $(DIST_DIR)/sms_tg_forwarder-arm64.zip
SRC_DIR ?= $(MODULE_DIR)/src

GO ?= go
GOOS ?= linux
GOARCH ?= arm64
CGO_ENABLED ?= 0
GOCACHE ?= $(CURDIR)/.cache/go-build

BUILD_TAGS ?= netgo osusergo sqlite_omit_load_extension
LDFLAGS ?= -s -w -extldflags "-static"

.PHONY: all build arm64 bootstrap clean deps tidy run test module magisk ksu zip

all: build

build: arm64

arm64:
	mkdir -p $(DIST_DIR)
	cd $(SRC_DIR) && GOCACHE=$(GOCACHE) CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build \
		-buildvcs=false \
		-trimpath \
		-tags '$(BUILD_TAGS)' \
		-ldflags '$(LDFLAGS)' \
		-o $(CURDIR)/$(TARGET) \
		.
	file $(TARGET) || true

test:
	cd $(SRC_DIR) && GOCACHE=$(GOCACHE) $(GO) test -v ./...

module: arm64
	mkdir -p $(MODULE_DIR)/system/bin
	cp $(TARGET) $(MODULE_DIR)/system/bin/$(APP)
	chmod 755 $(MODULE_DIR)/system/bin/$(APP)
	chmod 755 $(MODULE_DIR)/service.sh $(MODULE_DIR)/uninstall.sh
	test -f $(MODULE_DIR)/action.sh && chmod 755 $(MODULE_DIR)/action.sh || true
	test -f $(MODULE_DIR)/config.env && chmod 600 $(MODULE_DIR)/config.env || true

zip: module
	mkdir -p $(DIST_DIR)
	rm -f $(MODULE_ZIP)
	cd $(MODULE_DIR) && zip -r ../$(MODULE_ZIP) . -x 'src/*'
	ls -lh $(MODULE_ZIP)

magisk: zip
ksu: zip

bootstrap:
	cd $(SRC_DIR) && { test -f go.mod || $(GO) mod init $(MODULE); }
	cd $(SRC_DIR) && $(GO) get github.com/fsnotify/fsnotify modernc.org/sqlite
	cd $(SRC_DIR) && GOCACHE=$(GOCACHE) $(GO) mod tidy

deps:
	cd $(SRC_DIR) && $(GO) mod download

tidy:
	cd $(SRC_DIR) && GOCACHE=$(GOCACHE) $(GO) mod tidy

run:
	cd $(SRC_DIR) && GOCACHE=$(GOCACHE) $(GO) run .

clean:
	rm -rf $(DIST_DIR)
