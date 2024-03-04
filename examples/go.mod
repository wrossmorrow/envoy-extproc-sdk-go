module github.com/wrossmorrow/envoy-extproc-sdk-go/examples

go 1.21

require (
	github.com/google/uuid v1.6.0
	github.com/nqd/flat v0.2.0
	github.com/wrossmorrow/envoy-extproc-sdk-go v0.0.21
)

require (
	github.com/cncf/xds/go v0.0.0-20231128003011-0fa0005c9caa // indirect
	github.com/envoyproxy/go-control-plane v0.12.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.0.4 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	golang.org/x/net v0.20.0 // indirect
	golang.org/x/sys v0.16.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240125205218-1f4bbc51befe // indirect
	google.golang.org/grpc v1.61.0 // indirect
	google.golang.org/protobuf v1.32.0 // indirect
)

replace github.com/wrossmorrow/envoy-extproc-sdk-go => ../
