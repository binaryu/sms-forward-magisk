APP ?= sms-tg-forwarder
MODULE ?= sms-tg-forwarder
DIST_DIR ?= dist
TARGET ?= $(DIST_DIR)/$(APP)-linux-arm64
MAGISK_MODULE_DIR ?= sms_tg_forwarder
MAGISK_ZIP ?= $(DIST_DIR)/sms_tg_forwarder-magisk-arm64.zip
SRC_DIR ?= $(MAGISK_MODULE_DIR)/src

GO ?= go
GOOS ?= linux
GOARCH ?= arm64
CGO_ENABLED ?= 0
GOCACHE ?= $(CURDIR)/.cache/go-build

BUILD_TAGS ?= netgo osusergo sqlite_omit_load_extension
LDFLAGS ?= -s -w -extldflags "-static"

.PHONY: all build arm64 bootstrap clean deps tidy run module magisk

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

module:
	test -f $(TARGET) || $(MAKE) arm64
	mkdir -p $(MAGISK_MODULE_DIR)/system/bin
	cp $(TARGET) $(MAGISK_MODULE_DIR)/system/bin/$(APP)
	chmod 755 $(MAGISK_MODULE_DIR)/system/bin/$(APP)
	chmod 755 $(MAGISK_MODULE_DIR)/service.sh $(MAGISK_MODULE_DIR)/uninstall.sh
	chmod 600 $(MAGISK_MODULE_DIR)/config.env

magisk: module
	mkdir -p $(DIST_DIR)
	rm -f $(MAGISK_ZIP)
	cd $(MAGISK_MODULE_DIR) && zip -r ../$(MAGISK_ZIP) . -x 'src/*'
	ls -lh $(MAGISK_ZIP)

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
