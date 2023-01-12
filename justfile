
update:
    go get -u && just tidy

tidy:
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

tag VERSION="":
    git tag v{{VERSION}} \
        && git push origin --tags \
        && cd examples && just update

release VERSION="":
    git commit -m "release v{{VERSION}}" \
        && git push && just tag {{VERSION}}
