PACKAGES=$(shell go list ./... | grep -v '/vendor/')
COMMIT_HASH := $(shell git rev-parse --short HEAD)

COSMOS_RELEASE := $(shell grep 'github.com/BiJie/bnc-cosmos-sdk' Gopkg.toml -n1|grep version|awk '{print $$4}'| sed 's/\"//g')
TENDER_RELEASE := $(shell grep "github.com/BiJie/bnc-tendermint" Gopkg.toml -n1|grep version|awk '{print $$4}'| sed 's/\"//g')

BUILD_TAGS = netgo
BUILD_FLAGS = -tags "${BUILD_TAGS}" -ldflags "-X github.com/binance-chain/node/version.GitCommit=${COMMIT_HASH} -X github.com/binance-chain/node/version.CosmosRelease=${COSMOS_RELEASE} -X github.com/binance-chain/node/version.TendermintRelease=${TENDER_RELEASE}"
BUILD_TESTNET_FLAGS = ${BUILD_FLAGS} -ldflags "-X github.com/binance-chain/node/app.Bech32PrefixAccAddr=tbnb"

all: get_vendor_deps format build

########################################
### CI

ci: get_vendor_deps build

########################################
### Build

build:
ifeq ($(OS),Windows_NT)
	go build $(BUILD_FLAGS) -o build/bnbcli.exe ./cmd/bnbcli
	go build $(BUILD_TESTNET_FLAGS) -o build/tbnbcli.exe ./cmd/bnbcli
	go build $(BUILD_FLAGS) -o build/bnbchaind.exe ./cmd/bnbchaind
	go build $(BUILD_FLAGS) -o build/bnbsentry.exe ./cmd/bnbsentry
	go build $(BUILD_FLAGS) -o build/pressuremaker.exe ./cmd/pressuremaker
	go build $(BUILD_FLAGS) -o build/lightd.exe ./cmd/lightd
else
	go build $(BUILD_FLAGS) -o build/bnbcli ./cmd/bnbcli
	go build $(BUILD_TESTNET_FLAGS) -o build/tbnbcli ./cmd/bnbcli
	go build $(BUILD_FLAGS) -o build/bnbchaind ./cmd/bnbchaind
	go build $(BUILD_FLAGS) -o build/bnbsentry ./cmd/bnbsentry
	go build $(BUILD_FLAGS) -o build/pressuremaker ./cmd/pressuremaker
	go build $(BUILD_FLAGS) -o build/lightd ./cmd/lightd
endif

build-linux:
	LEDGER_ENABLED=false GOOS=linux GOARCH=amd64 $(MAKE) build

build-alpine:
	LEDGER_ENABLED=false GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(MAKE) build

install:
	go install $(BUILD_FLAGS) ./cmd/bnbchaind
	go install $(BUILD_FLAGS) ./cmd/bnbcli
	go install $(BUILD_FLAGS) ./cmd/bnbsentry

########################################
### Dependencies

get_vendor_deps:
	@rm -rf vendor/
	@echo "--> Running dep ensure"
	@dep ensure -v
	@go get golang.org/x/tools/cmd/goimports

########################################
### Format
format:
	@echo "-->Formatting"
	$(shell cd ../../../ && goimports -w -local github.com/binance-chain/node $(PACKAGES))
	$(shell cd ../../../ && gofmt -w $(PACKAGES))

########################################
### Lint
lint:
	@echo "-->Lint"
	golint $(PACKAGES)

########################################
### Testing

test: test_unit test_race

test_race:
	@go test -race $(PACKAGES)

test_unit:
	@go test $(PACKAGES)

integration_test: build
	@echo "-->Integration Test"
	@./integration_test.sh

########################################
### Pre Commit
pre_commit: build test format

########################################
### Local validator nodes using docker and docker-compose
build-docker-node:
	$(MAKE) -C networks/local

# Run a 4-node testnet locally
localnet-start: localnet-stop
	@if ! [ -f build/node0/gaiad/config/genesis.json ]; then docker run --rm -v $(CURDIR)/build:/bnbchaind:Z binance/bnbdnode testnet --v 4 -o . --starting-ip-address 172.20.0.2 ; fi
	@for i in `seq 0 3`; do \
		if [ "$(SKIP_TIMEOUT)" = "true" ]; then \
			sed -i -e "s/skip_timeout_commit = false/skip_timeout_commit = true/g" ./build/node$$i/gaiad/config/config.toml;\
		else \
			sed -i -e "s/skip_timeout_commit = true/skip_timeout_commit = false/g" ./build/node$$i/gaiad/config/config.toml;\
		fi;\
	done
	@for i in `seq 0 3`; do \
		if [ "$(PEX)" = "false" ]; then \
			sed -i -e "s/pex = true/pex = false/g" ./build/node$$i/gaiad/config/config.toml;\
		else \
			sed -i -e "s/pex = false/pex = true/g" ./build/node$$i/gaiad/config/config.toml;\
		fi;\
	done
	docker-compose up

# Stop testnet
localnet-stop:
	docker-compose down

# To avoid unintended conflicts with file names, always add to .PHONY
# unless there is a reason not to.
# https://www.gnu.org/software/make/manual/html_node/Phony-Targets.html
.PHONY: build install get_vendor_deps test test_unit build-linux build-docker-node localnet-start localnet-stop
