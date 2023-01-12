
update:
    go mod tidy

format:
    go fmt ./*.go

unit-test: 
    echo "TBD"

integration-test: 
    echo "TBD"

coverage: 
    echo "TBD"

run EXAMPLE="noop":
    cd examples && just run {{EXAMPLE}}

build *FLAGS="":
    go build {{FLAGS}}
