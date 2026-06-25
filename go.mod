module github.com/lxc/incus/v7

go 1.25.10

require (
	github.com/FuturFusion/vsock v0.0.0-20260219213046-d78a7104f821
	github.com/LINBIT/golinstor v0.63.0
	github.com/adhocore/gronx v1.20.0
	github.com/apex/log v1.9.0
	github.com/aws/aws-sdk-go-v2 v1.42.0
	github.com/aws/aws-sdk-go-v2/credentials v1.19.24
	github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager v0.2.11
	github.com/aws/aws-sdk-go-v2/service/s3 v1.104.0
	github.com/aws/smithy-go v1.27.2
	github.com/cenkalti/backoff/v5 v5.0.3
	github.com/checkpoint-restore/go-criu/v8 v8.3.0
	github.com/cowsql/go-cowsql v1.22.0
	github.com/digitalocean/go-smbios v0.0.0-20180907143718-390a4f403a8e
	github.com/dustinkirkland/golang-petname v0.0.0-20260215035315-f0c533e9ce9b
	github.com/fatih/color v1.19.0
	github.com/flosch/pongo2/v6 v6.1.0
	github.com/fvbommel/sortorder v1.1.0
	github.com/go-chi/chi/v5 v5.3.0
	github.com/go-jose/go-jose/v4 v4.1.4
	github.com/go-logr/logr v1.4.3
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/gopacket v1.1.19
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674
	github.com/gosexy/gettext v0.0.0-20160830220431-74466a0a0c4a
	github.com/insomniacslk/dhcp v0.0.0-20260603135910-a415979eb11e
	github.com/jaypipes/pcidb v1.1.1
	github.com/jochenvg/go-udev v0.0.0-20240801134859-b65ed646224b
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/lxc/go-lxc v0.0.0-20260316180011-3af4ce000ed7
	github.com/lxc/incus-os/incus-osd v0.0.0-20260625162138-012573a860bd
	github.com/mattn/go-colorable v0.1.15
	github.com/mattn/go-runewidth v0.0.24
	github.com/mattn/go-sqlite3 v1.14.47
	github.com/mdlayher/arp v0.0.0-20260528070854-93566ba168e9
	github.com/mdlayher/ndp v1.1.0
	github.com/mdlayher/netx v0.0.0-20230430222610-7e21880baee8
	github.com/mdlayher/packet v1.1.2
	github.com/mdlayher/vsock v1.3.0
	github.com/miekg/dns v1.1.72
	github.com/mitchellh/mapstructure v1.5.0
	github.com/olekukonko/tablewriter v1.1.4
	github.com/opencontainers/runtime-spec v1.3.0
	github.com/opencontainers/selinux v1.15.1
	github.com/opencontainers/umoci v0.6.1-0.20251213054154-70fc5ee1f4df
	github.com/openfga/go-sdk v0.8.2
	github.com/osrg/gobgp/v4 v4.6.0
	github.com/ovn-kubernetes/libovsdb v0.8.1
	github.com/pires/go-proxyproto v0.12.0
	github.com/pkg/sftp v1.13.10
	github.com/pkg/xattr v0.4.12
	github.com/shirou/gopsutil/v4 v4.26.5
	github.com/sirupsen/logrus v1.9.4
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/stretchr/testify v1.11.1
	github.com/vishvananda/netlink v1.3.1
	github.com/zitadel/oidc/v3 v3.47.5
	go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb
	go.yaml.in/yaml/v4 v4.0.0-rc.6
	golang.org/x/crypto v0.53.0
	golang.org/x/exp v0.0.0-20260611194520-c48552f49976
	golang.org/x/net v0.56.0
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.21.0
	golang.org/x/sys v0.46.0
	golang.org/x/term v0.44.0
	golang.org/x/text v0.38.0
	golang.org/x/tools v0.47.0
	google.golang.org/protobuf v1.36.11
	k8s.io/utils v0.0.0-20260617174310-a95e086a2553
	software.sslmate.com/src/go-pkcs12 v0.7.3
)

require (
	cyphar.com/go-pathrs v0.2.5 // indirect
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20240806141605-e8a1dd7889d6 // indirect
	github.com/Rican7/retry v0.3.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.13 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.30 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.29 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.29 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/hub v1.0.2 // indirect
	github.com/cenkalti/rpc2 v1.0.5 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/platforms v1.0.0-rc.4 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/cyphar/filepath-securejoin v0.7.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgryski/go-farm v0.0.0-20240924180020-3414d57e47da // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/donovanhide/eventsource v0.0.0-20210830082556-c59027999da0 // indirect
	github.com/eapache/channels v1.1.0 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/ebitengine/purego v0.10.1 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/gaissmai/bart v0.28.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.30.3 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/google/renameio v1.0.1 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jkeiser/iter v0.0.0-20200628201005-c8aa0ae784d1 // indirect
	github.com/josharian/native v1.1.0 // indirect
	github.com/k-sone/critbitgo v1.4.0 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/klauspost/pgzip v1.2.6 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20260330125221-c963978e514e // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/mdlayher/ethernet v0.0.0-20220221185849-529eae5b6118 // indirect
	github.com/mdlayher/socket v0.6.1 // indirect
	github.com/moby/sys/user v0.4.1 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/muhlemmer/gu v0.3.1 // indirect
	github.com/muhlemmer/httpforwarded v0.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/olekukonko/cat v0.0.0-20250911104152-50322a0618f6 // indirect
	github.com/olekukonko/errors v1.3.0 // indirect
	github.com/olekukonko/ll v0.1.8 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/orcaman/concurrent-map/v2 v2.0.1 // indirect
	github.com/pelletier/go-toml/v2 v2.4.2 // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.69.0 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/rootless-containers/proto/go-proto v0.0.0-20260207013450-f6ee952d53d9 // indirect
	github.com/rs/cors v1.11.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/segmentio/fasthash v1.0.3 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	github.com/urfave/cli v1.22.17 // indirect
	github.com/vbatts/go-mtree v0.7.0 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	github.com/zitadel/logging v0.7.0 // indirect
	github.com/zitadel/schema v1.3.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260622175928-b703f567277d // indirect
	google.golang.org/grpc v1.81.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	moul.io/http2curl/v2 v2.3.0 // indirect
)
