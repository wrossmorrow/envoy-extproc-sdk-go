
image_name := "envoy-extproc-sdk-go"
image_tag := `git rev-parse HEAD`
generated_code := "gen/proto/go"

install: codegen

update: buf-update codegen

buf-update :
    buf mod update

codegen:
    buf -v generate buf.build/cncf/xds
    buf -v generate buf.build/envoyproxy/envoy
    buf -v generate buf.build/envoyproxy/protoc-gen-validate
    buf -v generate https://github.com/grpc/grpc.git --path src/proto/grpc/health/v1/health.proto

format:
    go fmt src/*.go

unit-test: 
    echo "TBD"

integration-test: 
    echo "TBD"

coverage: 
    echo "TBD"

run:
    go run src/*.go

build:
    docker build . -t {{image_name}}:{{image_tag}}
    docker build . -f examples/Dockerfile \
        --build-arg IMAGE_TAG=$(IMAGE_TAG) \
        -t {{image_name}}-examples:{{image_tag}}

up:
    docker-compose up --build

down:
    docker-compose down --volumes

package:
    poetry build

publish:
    poetry publish -r pypi --build
