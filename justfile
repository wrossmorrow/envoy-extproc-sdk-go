
default:
    just --list

# update dependendencies
update:
    go get -u && just tidy

# cleanup modules
tidy:
    go mod tidy

# format code
format:
    go fmt ./*.go

# run unit tests (TBD)
unit-test: 
    echo "TBD" && exit 1

# run integration tests (TBD)
integration-test: 
    echo "TBD" && exit 1

# run tests with coverage (TBD)
coverage: 
    echo "TBD" && exit 1

# run a specific example
run example="noop":
    cd examples && just run {{example}}

# build binary (variadic flags supported)
build *flags="":
    go build {{flags}}

# tag for a release
tag version="":
    git tag v{{version}} && git push origin --tags

# release a new version, via a specific commit (deprecate)
release version="":
    git commit -m "release v{{version}}" \
        && git push && just tag {{version}}
