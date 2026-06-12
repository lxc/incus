GO ?= go
DOMAIN=incus
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot
VERSION=$(or ${CUSTOM_VERSION},$(shell grep "var Version" internal/version/flex.go | cut -d'"' -f2))
ARCHIVE=incus-$(VERSION).tar
HASH := \#
TAG_SQLITE3=$(shell printf "$(HASH)include <cowsql.h>\nvoid main(){cowsql_node_id n = 1;}" | $(CC) ${CGO_CFLAGS} -o /dev/null -xc - >/dev/null 2>&1 && echo "libsqlite3")
GOPATH ?= $(shell $(GO) env GOPATH)
CGO_LDFLAGS_ALLOW ?= (-Wl,-wrap,pthread_create)|(-Wl,-z,now)
SPHINXENV=doc/.sphinx/venv/bin/activate
SPHINXPIPPATH=doc/.sphinx/venv/bin/pip
OVN_MINVER=23.03.0
OVS_MINVER=2.15.0

ifneq "$(wildcard vendor)" ""
	RAFT_PATH=$(CURDIR)/vendor/raft
	COWSQL_PATH=$(CURDIR)/vendor/cowsql
else
	RAFT_PATH=$(GOPATH)/deps/raft
	COWSQL_PATH=$(GOPATH)/deps/cowsql
endif

# section(Build): Build Incus

.PHONY: default
default: build

.PHONY: build
# doc: Build all Incus binaries (same as make and make default)
build:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing cowsql, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 $(GO) install -v -tags netgo ./cmd/incus-migrate
	CGO_ENABLED=0 $(GO) install -v -tags agent,netgo ./cmd/incus-agent
	@echo "Incus built successfully"

.PHONY: client
# doc: Build the Incus client
client:
	$(GO) install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./cmd/incus
	@echo "Incus client built successfully"

.PHONY: incus-agent
# doc: Build the Incus agent
incus-agent:
	CGO_ENABLED=0 $(GO) install -v -tags agent,netgo ./cmd/incus-agent
	@echo "Incus agent built successfully"

.PHONY: incus-migrate
# doc: Build the Incus migration tool
incus-migrate:
	CGO_ENABLED=0 $(GO) install -v -tags netgo ./cmd/incus-migrate
	@echo "Incus migration tool built successfully"

.PHONY: debug
# doc: Build Incus in debug mode
debug:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -v -tags "$(TAG_SQLITE3) logdebug" $(DEBUG) ./...
	CGO_ENABLED=0 $(GO) install -v -tags "netgo,logdebug" ./cmd/incus-migrate
	CGO_ENABLED=0 $(GO) install -v -tags "agent,netgo,logdebug" ./cmd/incus-agent
	@echo "Incus built successfully"

.PHONY: nocache
# doc: Build Incus ignoring the local Go cache
nocache:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -a -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 $(GO) install -a -v -tags netgo ./cmd/incus-migrate
	CGO_ENABLED=0 $(GO) install -a -v -tags agent,netgo ./cmd/incus-agent
	@echo "Incus built successfully"

.PHONY: race
# doc: Build Incus in race condition detection mode
race:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) install -race -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 $(GO) install -v -tags netgo ./cmd/incus-migrate
	CGO_ENABLED=0 $(GO) install -v -tags agent,netgo ./cmd/incus-agent
	@echo "Incus built successfully"

# section(Dependencies): Manage Incus dependencies

.PHONY: deps
# doc: Build Incus dependencies
deps:
	@if [ ! -e "$(RAFT_PATH)" ]; then \
		git clone --depth=1 "https://github.com/cowsql/raft" "$(RAFT_PATH)"; \
	elif [ -e "$(RAFT_PATH)/.git" ]; then \
		cd "$(RAFT_PATH)"; git pull; \
	fi

	cd "$(RAFT_PATH)" && \
		autoreconf -i && \
		./configure && \
		make

	# cowsql
	@if [ ! -e "$(COWSQL_PATH)" ]; then \
		git clone --depth=1 "https://github.com/cowsql/cowsql" "$(COWSQL_PATH)"; \
	elif [ -e "$(COWSQL_PATH)/.git" ]; then \
		cd "$(COWSQL_PATH)"; git pull; \
	fi

	cd "$(COWSQL_PATH)" && \
		autoreconf -i && \
		PKG_CONFIG_PATH="$(RAFT_PATH)" ./configure && \
		make CFLAGS="-I$(RAFT_PATH)/include/" LDFLAGS="-L$(RAFT_PATH)/.libs/"

	# environment
	@echo ""
	@echo "Please set the following in your environment (possibly ~/.bashrc)"
	@echo "export CGO_CFLAGS=\"-I$(RAFT_PATH)/include/ -I$(COWSQL_PATH)/include/\""
	@echo "export CGO_LDFLAGS=\"-L$(RAFT_PATH)/.libs -L$(COWSQL_PATH)/.libs/\""
	@echo "export LD_LIBRARY_PATH=\"$(RAFT_PATH)/.libs/:$(COWSQL_PATH)/.libs/\""
	@echo "export CGO_LDFLAGS_ALLOW=\"(-Wl,-wrap,pthread_create)|(-Wl,-z,now)\""

.PHONY: update-gomod
# doc: Update Go dependencies
update-gomod:
ifneq "$(INCUS_OFFLINE)" ""
	@echo "The update-gomod target cannot be run in offline mode."
	exit 1
endif
	$(GO) get -t -v -u ./...
	$(GO) mod tidy --go=1.25.10
	$(GO) get toolchain@none

	@echo "Dependencies updated"

# section(Schemas): Update Incus data schemas

.PHONY: update-ovsdb
# doc: Update OVSDB schema
update-ovsdb:
	go install github.com/ovn-kubernetes/libovsdb/cmd/modelgen@main

	rm -Rf internal/server/network/ovs/schema
	mkdir internal/server/network/ovs/schema
	curl -s https://raw.githubusercontent.com/openvswitch/ovs/v$(OVS_MINVER)/vswitchd/vswitch.ovsschema -o internal/server/network/ovs/schema/ovs.json
	modelgen -o internal/server/network/ovs/schema/ovs internal/server/network/ovs/schema/ovs.json
	rm internal/server/network/ovs/schema/*.json

	rm -Rf internal/server/network/ovn/schema
	mkdir internal/server/network/ovn/schema
	curl -s https://raw.githubusercontent.com/ovn-org/ovn/v$(OVN_MINVER)/ovn-nb.ovsschema -o internal/server/network/ovn/schema/ovn-nb.json
	curl -s https://raw.githubusercontent.com/ovn-org/ovn/v$(OVN_MINVER)/ovn-sb.ovsschema -o internal/server/network/ovn/schema/ovn-sb.json
	curl -s https://raw.githubusercontent.com/ovn-org/ovn/v$(OVN_MINVER)/ovn-ic-nb.ovsschema -o internal/server/network/ovn/schema/ovn-ic-nb.json
	curl -s https://raw.githubusercontent.com/ovn-org/ovn/v$(OVN_MINVER)/ovn-ic-sb.ovsschema -o internal/server/network/ovn/schema/ovn-ic-sb.json
	modelgen -o internal/server/network/ovn/schema/ovn-nb internal/server/network/ovn/schema/ovn-nb.json
	modelgen -o internal/server/network/ovn/schema/ovn-sb internal/server/network/ovn/schema/ovn-sb.json
	modelgen -o internal/server/network/ovn/schema/ovn-ic-nb internal/server/network/ovn/schema/ovn-ic-nb.json
	modelgen -o internal/server/network/ovn/schema/ovn-ic-sb internal/server/network/ovn/schema/ovn-ic-sb.json
	rm internal/server/network/ovn/schema/*.json

.PHONY: update-protobuf
# doc: Update Protobuf schema
update-protobuf:
	protoc --go_out=. ./internal/migration/migrate.proto

.PHONY: update-schema
# doc: Update database schema
update-schema:
	cd cmd/generate-database && $(GO) build -o $(GOPATH)/bin/generate-database -tags "$(TAG_SQLITE3)" $(DEBUG) && cd -
	$(GO) generate ./...
	gofumpt -w ./internal/server/db/
	goimports -w ./internal/server/db/
	@echo "Code generation completed"

.PHONY: update-api
# doc: Update API schema
update-api:
ifeq "$(INCUS_OFFLINE)" ""
	(cd / ; $(GO) install -v -x github.com/go-swagger/go-swagger/cmd/swagger@master)
endif
	swagger generate spec -o doc/rest-api.yaml -w ./cmd/incusd -m

.PHONY: update-metadata
# doc: Update configuration metadata
update-metadata: build
	@echo "Generating golang documentation metadata"
	cd cmd/generate-config && CGO_ENABLED=0 $(GO) build -o $(GOPATH)/bin/generate-config
	$(GOPATH)/bin/generate-config . --json ./internal/server/metadata/configuration.json --txt ./doc/config_options.txt

# OpenFGA Syntax Transformer: https://github.com/openfga/syntax-transformer
.PHONY: update-openfga
# doc: Update OpenFGA schema
update-openfga:
ifeq ($(shell command -v fga),)
	(cd / ; $(GO) install -v -x github.com/openfga/cli/cmd/fga@latest)
endif
	@printf 'package auth\n\n// Code generated by Makefile; DO NOT EDIT.\n\nvar authModel = `%s`\n' '$(shell fga model transform --file=./internal/server/auth/driver_openfga_model.openfga | jq -c)' > ./internal/server/auth/driver_openfga_model.go

# section(Documentation): Build Incus documentation

.PHONY: doc
# doc: Setup the build environment and build the documentation
doc: doc-setup doc-incremental

.PHONY: doc-setup
# doc: Setup a documentation build environment
doc-setup: client
	@echo "Setting up documentation build environment"
	python3 -m venv doc/.sphinx/venv
	. $(SPHINXENV) ; pip install --require-virtualenv --upgrade -r doc/.sphinx/requirements.txt --log doc/.sphinx/venv/pip_install.log
	@test ! -f doc/.sphinx/venv/pip_list.txt || \
        mv doc/.sphinx/venv/pip_list.txt doc/.sphinx/venv/pip_list.txt.bak
	$(SPHINXPIPPATH) list --local --format=freeze > doc/.sphinx/venv/pip_list.txt
	find doc/reference/manpages/ -name "*.md" -type f -delete
	rm -Rf doc/html
	rm -Rf doc/.sphinx/.doctrees

.PHONY: doc-incremental
# doc: Build the documentation
doc-incremental:
	@echo "Build the documentation"
	. $(SPHINXENV) ; NO_COLOR=1 sphinx-build -c doc/ -b dirhtml doc/ doc/html/ -d doc/.sphinx/.doctrees -w doc/.sphinx/warnings.txt

.PHONY: doc-serve
# doc: Serve the documentation on localhost:8001
doc-serve:
	cd doc/html; python3 -m http.server 8001

.PHONY: doc-spellcheck
# doc: Check spelling errors on the documentation
doc-spellcheck: doc
	. $(SPHINXENV) ; python3 -m pyspelling -c doc/.sphinx/spellingcheck.yaml

.PHONY: doc-spellcheck-incremental
# doc: Check spelling errors on the documentation, building the documentation only
doc-spellcheck-incremental: doc-incremental
	. $(SPHINXENV) ; python3 -m pyspelling -c doc/.sphinx/spellingcheck.yaml

.PHONY: doc-linkcheck
# doc: Check broken links on the documentation
doc-linkcheck: doc-setup
	. $(SPHINXENV) ; LOCAL_SPHINX_BUILD=True sphinx-build -c doc/ -b linkcheck doc/ doc/html/ -d doc/.sphinx/.doctrees

.PHONY: doc-lint
# doc: Lint the documentation
doc-lint:
	doc/.sphinx/.markdownlint/doc-lint.sh

.PHONY: woke-install
# doc: Install the inclusive checker
woke-install:
	@type woke >/dev/null 2>&1 || \
        { echo "Installing \"woke\" snap... \n"; sudo snap install woke; }

.PHONY: doc-woke
# doc: Check for non-inclusive phrasing
doc-woke: woke-install
	woke *.md **/*.md -c https://github.com/canonical/Inclusive-naming/raw/main/config.yml

# section(Tests): Run the tests

.PHONY: check
# doc: Run the test suite
check: default
ifeq "$(INCUS_OFFLINE)" ""
	(cd / ; $(GO) install -v -x github.com/rogpeppe/godeps@latest)
	(cd / ; $(GO) install -v -x github.com/tsenart/deadcode@latest)
	(cd / ; $(GO) install -v -x golang.org/x/lint/golint@latest)
endif
	CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" $(GO) test -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	cd test && ./main.sh

.PHONY: static-analysis
# doc: Run static analysis
static-analysis:
ifeq ($(shell command -v go-licenses),)
	(cd / ; $(GO) install -v -x github.com/google/go-licenses@latest)
endif
ifeq ($(shell command -v govulncheck),)
	go install golang.org/x/vuln/cmd/govulncheck@latest
endif
ifeq ($(shell command -v golangci-lint),)
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $$($(GO) env GOPATH)/bin
endif
ifeq ($(shell command -v shellcheck),)
	echo "Please install shellcheck"
	exit 1
endif
ifeq ($(shell command -v flake8),)
	echo "Please install flake8"
	exit 1
endif
ifeq ($(shell command -v codespell),)
	echo "Please install codespell"
	exit 1
endif
ifeq ($(shell command -v run-parts),)
	echo "Please install run-parts"
	exit 1
endif
	flake8 test/deps/import-busybox
	shellcheck --shell sh test/*.sh test/includes/*.sh test/suites/*.sh test/backends/*.sh test/lint/*.sh
	shellcheck test/extras/*.sh
	run-parts $(shell run-parts -V >/dev/null 2>&1 && echo -n "--verbose --exit-on-error --regex '.sh'") test/lint

.PHONY: staticcheck
# doc: Run static checks
staticcheck:
ifeq ($(shell command -v staticcheck),)
	(cd / ; $(GO) install -v -x honnef.co/go/tools/cmd/staticcheck@latest)
endif
	# To get advance notice of deprecated function usage, consider running:
	#   sed -i 's/^go 1\.[0-9]\+$/go 1.18/' go.mod
	# before 'make staticcheck'.

	# Run staticcheck against all the dirs containing Go files.
	staticcheck $$(git ls-files *.go | sed 's|^|./|; s|/[^/]\+\.go$$||' | sort -u)

.PHONY: unit-test
# doc: Run unit tests
unit-test:
	sudo --preserve-env=CGO_CFLAGS,CGO_LDFLAGS,CGO_LDFLAGS_ALLOW,LD_LIBRARY_PATH LD_LIBRARY_PATH=${LD_LIBRARY_PATH} env "PATH=${PATH}" $(GO) test ./...

# section(Internationalization): Generate internationalization files

.PHONY: i18n
# doc: Generate internationalization files
i18n: update-pot update-po

po/%.mo: po/%.po
	msgfmt --statistics -o $@ $<

po/%.po: po/$(DOMAIN).pot
	msgmerge -U po/$*.po po/$(DOMAIN).pot

.PHONY: update-po
# doc: Update PO files
update-po:
	set -eu; \
	for lang in $(LINGUAS); do\
	    msgmerge --backup=none -U $$lang.po po/$(DOMAIN).pot; \
	done

.PHONY: update-pot
# doc: Update POT file
update-pot:
ifeq "$(INCUS_OFFLINE)" ""
	(cd / ; $(GO) install -v -x github.com/snapcore/snapd/i18n/xgettext-go@2.57.1)
endif
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxc-devel@lists.linuxcontainers.org --keyword=i18n.G --keyword-plural=i18n.NG cmd/incus/*.go cmd/incus/color/*.go cmd/incus/usage/*.go shared/cliconfig/*.go
	sed -i s/CHARSET/UTF-8/ po/$(DOMAIN).pot

.PHONY: build-mo
# doc! Build MO files
build-mo: $(MOFILES)

# section(Miscellaneous): Targets that don’t fit in any category

.PHONY: dist
# doc: Prepare a release tarball
dist: doc
	# Cleanup
	rm -Rf $(ARCHIVE).xz

	# Create build dir
	$(eval TMP := $(shell mktemp -d))
	git archive --prefix=incus-$(VERSION)/ HEAD | tar -x -C $(TMP)
	git show-ref HEAD | cut -d' ' -f1 > $(TMP)/incus-$(VERSION)/.gitref

	# Download dependencies
	(cd $(TMP)/incus-$(VERSION) ; $(GO) mod vendor)

	# Download the cowsql libraries
	git clone --depth=1 https://github.com/cowsql/cowsql $(TMP)/incus-$(VERSION)/vendor/cowsql
	(cd $(TMP)/incus-$(VERSION)/vendor/cowsql ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	git clone --depth=1 https://github.com/cowsql/raft $(TMP)/incus-$(VERSION)/vendor/raft
	(cd $(TMP)/incus-$(VERSION)/vendor/raft ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	# Copy doc output
	cp -r doc/html $(TMP)/incus-$(VERSION)/doc/html/

	# Assemble tarball
	tar --exclude-vcs -C $(TMP) -Jcf $(ARCHIVE).xz incus-$(VERSION)/

	# Cleanup
	rm -Rf $(TMP)

.PHONY: help
# doc: Show this help
help:
	@echo The following targets are supported:
	@sed -En 's/^#\s*section\(([^)]*)\):\s*(.*)$$/\n\x1b[1m\1:\x1b[0m \2/p;/^\.PHONY:/{N;N;s/^\.PHONY:\s*([^[:space:]]+)\n#\s*doc(:\s*(.*)\n\1:\s*$$|!\s*(.*)\n\1:[^\n]*)/  \1!\3\4/p;s/^\.PHONY:\s*([^[:space:]]+)\s*\n#\s*doc:\s*(.*)\n\1:\s*(.+)$$/  \1!\2 (runs \3)/p}' Makefile | awk -F! '{if(NF<2)print$$1;else{s=$$1;if(length(s)%2)s=s" ";while(length(s)<28)s=s" .";print s" "$$2}}'
