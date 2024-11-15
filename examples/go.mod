module github.com/wrossmorrow/envoy-extproc-sdk-go/examples

go 1.22.7

toolchain go1.23.3

require (
	github.com/google/uuid v1.6.0
	github.com/nqd/flat v0.2.0
	github.com/wrossmorrow/envoy-extproc-sdk-go v0.0.22
)

require (
	github.com/cncf/xds/go v0.0.0-20240905190251-b4127c9b8d78 // indirect
	github.com/envoyproxy/go-control-plane v0.13.1 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.1.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	golang.org/x/net v0.31.0 // indirect
	golang.org/x/sys v0.27.0 // indirect
	golang.org/x/text v0.20.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241113202542-65e8d215514f // indirect
	google.golang.org/grpc v1.68.0 // indirect
	google.golang.org/protobuf v1.35.2 // indirect
)

replace github.com/wrossmorrow/envoy-extproc-sdk-go => ../
